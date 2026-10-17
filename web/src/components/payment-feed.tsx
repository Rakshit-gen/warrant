// A strip under the hero: agent payments going past, each marked with
// what warrant did. Example lines, not data. CSS only; it stops for
// reduced motion.

const rows: [string, string, string, "paid" | "asked" | "denied"][] = [
  ["₹412", "Zepto", "atta, dal, milk", "paid"],
  ["₹2,000", "Zepto", "gift card in the cart", "denied"],
  ["₹799", "airtel-bill.pay@okaxis", "not Airtel's UPI ID", "denied"],
  ["₹386", "Blinkit", "vegetables", "paid"],
  ["₹4,180", "Zepto", "monthly stock-up", "asked"],
  ["₹1,450", "BESCOM", "electricity bill", "paid"],
  ["₹950", "Zepto", "fourth order in ten minutes", "asked"],
  ["₹3,600", "zeptoo.store@okaxis", "lookalike store", "denied"],
  ["₹268", "Blinkit", "bread, eggs", "paid"],
  ["₹6,900", "Zepto", "total higher than the cart", "denied"],
  ["₹799", "Airtel", "mobile recharge", "paid"],
];

const word = { paid: "paid", asked: "asked the user", denied: "refused" };
const tone = { paid: "text-paid", asked: "text-asked", denied: "text-denied" };

export function PaymentFeed() {
  return (
    <div className="relative left-1/2 w-screen -translate-x-1/2 border-y border-rule bg-sheet">
      <div className="flex items-stretch">
        <p className="z-10 hidden shrink-0 border-r border-rule bg-sheet px-5 py-3 text-[0.8rem] text-ink-faint sm:block">
          Examples of what it does
        </p>
        <div className="overflow-hidden" aria-label="Example payments and what warrant did with them">
          <ul className="feed flex w-max text-[0.88rem]">
            {[...rows, ...rows].map(([amt, to, what, out], i) => (
              <li key={i} aria-hidden={i >= rows.length} className="flex items-baseline gap-2 border-r border-rule px-5 py-3 whitespace-nowrap">
                <span className="num font-bold text-ink">{amt}</span>
                <span className="text-ink-soft">
                  to {to}, {what}:
                </span>
                <span className={`font-bold ${tone[out]}`}>{word[out]}</span>
              </li>
            ))}
          </ul>
        </div>
      </div>
    </div>
  );
}
