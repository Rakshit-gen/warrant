package eval

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/Rakshit-gen/warrant/client"
	"github.com/Rakshit-gen/warrant/policy"
	"github.com/Rakshit-gen/warrant/sign"
	"github.com/Rakshit-gen/warrant/wire"
)

// Clock is a clock the runner moves. warrantd and the agents both read it,
// so weeks of orders replay in seconds.
type Clock struct {
	mu sync.Mutex
	t  time.Time
}

func NewClock(t time.Time) *Clock { return &Clock{t: t} }

func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// What happened to one payment attempt.
const (
	Paid     = "paid"      // went through without asking the user
	Asked    = "asked"     // warrant asked the user (who said yes if they wanted it, no if not)
	Denied   = "denied"    // policy refused
	Rejected = "rejected"  // never reached policy: bad signature, replay
	NoChange = "duplicate" // answered as an earlier request, nothing new paid
)

// Outcome is one payment attempt.
type Outcome struct {
	Scenario  string   `json:"scenario"`
	Step      int      `json:"step"`
	Malicious bool     `json:"malicious"`
	Amount    int64    `json:"amount"`
	Result    string   `json:"result"`
	MoneyPaid int64    `json:"money_paid"` // after the user's answer, if asked
	Reasons   []string `json:"reasons,omitempty"`
}

// Run plays the scenarios against warrantd at api. Registration needs
// api.Token if the server has one. prefix keeps IDs apart between runs on
// one database.
func Run(ctx context.Context, api *client.API, clock *Clock, scenarios []Scenario, prefix string) ([]Outcome, error) {
	for id, vpa := range Merchants {
		if err := api.AddMerchant(ctx, id, id, vpa); err != nil && !isStatus(err, 409) {
			return nil, err
		}
	}
	var out []Outcome
	for i, sc := range scenarios {
		o, err := runOne(ctx, api, clock, sc, fmt.Sprintf("%s%d", prefix, i))
		if err != nil {
			return out, fmt.Errorf("%s: %w", sc.ID, err)
		}
		out = append(out, o...)
		// Sessions are independent; a gap keeps one from showing in the
		// next one's per-day counts.
		clock.Advance(48 * time.Hour)
	}
	return out, nil
}

func runOne(ctx context.Context, api *client.API, clock *Clock, sc Scenario, id string) ([]Outcome, error) {
	dev := client.Device{API: api, User: "u-" + id, Key: sign.NewKey()}
	ag := client.Agent{API: api, ID: "a-" + id, Key: sign.NewKey()}
	if err := api.AddUser(ctx, dev.User, dev.User, dev.Key); err != nil {
		return nil, err
	}
	if err := api.AddAgent(ctx, ag.ID, ag.ID, "dev-"+sc.Family, ag.Key); err != nil {
		return nil, err
	}
	scope := sc.Scope
	scope.ID, scope.Agent = "m-"+id, ag.ID
	now := clock.Now()
	scope.NotBefore, scope.Expires = now.Add(-time.Minute).Unix(), now.Add(time.Duration(sc.Days)*24*time.Hour).Unix()
	if _, err := dev.Grant(ctx, scope); err != nil {
		return nil, err
	}

	var out []Outcome
	var last *sign.Envelope
	for n, st := range sc.Steps {
		clock.Advance(st.After)
		o := Outcome{Scenario: sc.ID, Step: n, Malicious: st.Malicious}
		var res client.Result
		var err error
		switch st.Act {
		case Revoke:
			if err := dev.Revoke(ctx, scope.ID); err != nil {
				return nil, err
			}
			continue
		case Replay:
			if last == nil {
				return nil, errors.New("replay with nothing sent")
			}
			res, err = ag.Send(ctx, *last, nil)
		case Pay, Forge:
			req, intent, e := request(dev, ag, clock, scope.ID, n, st)
			if e != nil {
				return nil, e
			}
			o.Amount = req.Amount
			key := ag.Key
			if st.Act == Forge {
				key = sign.NewKey()
			}
			env, e := key.Seal(req)
			if e != nil {
				return nil, e
			}
			if !st.Malicious {
				last = &env
			}
			res, err = ag.Send(ctx, env, intent)
		default:
			return nil, fmt.Errorf("unknown act %q", st.Act)
		}

		switch {
		case isStatus(err, 401) || isStatus(err, 409):
			o.Result = Rejected
		case err != nil:
			return nil, err
		case res.Duplicate:
			o.Result = NoChange
		default:
			for _, r := range res.Reasons {
				o.Reasons = append(o.Reasons, r.Code)
			}
			switch res.Outcome {
			case policy.Approve:
				o.Result = Paid
			case policy.Deny:
				o.Result = Denied
			case policy.StepUp:
				o.Result = Asked
				// The user says yes to what they wanted and no to what
				// they did not. That they always notice is an assumption.
				pending, err := dev.Pending(ctx)
				if err != nil {
					return nil, err
				}
				for _, p := range pending {
					if p.ID == *res.StepUp {
						if _, err := dev.Answer(ctx, p, !st.Malicious); err != nil && !isStatus(err, 409) {
							return nil, err
						}
					}
				}
				if res, err = api.Poll(ctx, res.Request); err != nil {
					return nil, err
				}
			}
			if res.Debit != nil && res.Debit.Status == "succeeded" {
				o.MoneyPaid = res.Debit.Amount
			}
		}
		out = append(out, o)
	}
	return out, nil
}

func request(dev client.Device, ag client.Agent, clock *Clock, mandate string, n int, st Step) (wire.DebitRequest, *sign.Envelope, error) {
	req := wire.DebitRequest{Type: "debit", Mandate: mandate, Agent: ag.ID, Nonce: client.Nonce(), TS: clock.Now().UnixMilli(),
		IdemKey: fmt.Sprintf("%s-%d", mandate, n), Merchant: st.Merchant, PayeeVPA: st.PayeeVPA, Amount: st.Amount, Cart: st.Cart}
	if req.Amount == 0 {
		req.Amount = total(st.Cart)
	}
	if st.Intent == nil {
		return req, nil, nil
	}
	ir := *st.Intent
	ir.ID, ir.Agent, ir.Mandate = fmt.Sprintf("int-%s-%d", mandate, n), ag.ID, mandate
	ir.Expires = clock.Now().Add(2 * time.Hour).Unix()
	env, err := dev.Intent(ir)
	req.IntentID = ir.ID
	return req, &env, err
}

func isStatus(err error, code int) bool {
	var e *client.Error
	return errors.As(err, &e) && e.Status == code
}

// Tally is one family's numbers under one policy.
type Tally struct {
	Family string `json:"family"`
	Kind   string `json:"kind"`
	What   string `json:"what"`
	// Payment attempts the user wanted.
	Wanted        int `json:"wanted"`
	WantedPaid    int `json:"wanted_paid"`    // without asking
	WantedAsked   int `json:"wanted_asked"`   // the user had to confirm
	WantedRefused int `json:"wanted_refused"` // denied or rejected: a false block
	// Payment attempts the user did not want.
	Unwanted        int   `json:"unwanted"`
	UnwantedPaid    int   `json:"unwanted_paid"`    // went through silently
	UnwantedAsked   int   `json:"unwanted_asked"`   // stopped at the user
	UnwantedStopped int   `json:"unwanted_stopped"` // denied, rejected or deduplicated
	MoneyTried      int64 `json:"money_tried"`
	MoneyLost       int64 `json:"money_lost"`
	// Why warrant stopped or asked, by reason code.
	Reasons map[string]int `json:"reasons"`
}

// Summarize groups outcomes by family.
func Summarize(scenarios []Scenario, outs []Outcome) []Tally {
	fam := map[string]string{}
	for _, s := range scenarios {
		fam[s.ID] = s.Family
	}
	by := map[string]*Tally{}
	for _, f := range Families {
		by[f.Name] = &Tally{Family: f.Name, Kind: f.Kind, What: f.What, Reasons: map[string]int{}}
	}
	for _, o := range outs {
		t := by[fam[o.Scenario]]
		if t == nil {
			continue
		}
		if !o.Malicious {
			t.Wanted++
			switch o.Result {
			case Paid:
				t.WantedPaid++
			case Asked:
				t.WantedAsked++
			case Denied, Rejected:
				t.WantedRefused++
			}
			continue
		}
		t.Unwanted++
		t.MoneyTried += o.Amount
		t.MoneyLost += o.MoneyPaid
		switch o.Result {
		case Paid:
			t.UnwantedPaid++
		case Asked:
			t.UnwantedAsked++
		default:
			t.UnwantedStopped++
		}
		for _, r := range o.Reasons {
			t.Reasons[r]++
		}
	}
	out := make([]Tally, 0, len(by))
	for _, f := range Families {
		out = append(out, *by[f.Name])
	}
	sort.SliceStable(out, func(i, j int) bool { return kindOrder(out[i].Kind) < kindOrder(out[j].Kind) })
	return out
}

func kindOrder(k string) int {
	switch k {
	case Benign:
		return 0
	case Attack:
		return 1
	}
	return 2
}
