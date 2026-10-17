//go:build js && wasm

// Command verify-wasm is the evidence checker built for the browser. The
// website loads it to check a pack on the visitor's machine with the same
// code as warrant verify, and to run the policy on requests a visitor
// builds in the playground.
package main

import (
	"encoding/json"
	"syscall/js"

	"github.com/Rakshit-gen/warrant/evidence"
	"github.com/Rakshit-gen/warrant/policy"
)

func main() {
	js.Global().Set("warrantVerify", js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) < 2 {
			return `{"error":"warrantVerify(packJSON, claim)"}`
		}
		var p evidence.Pack
		if err := json.Unmarshal([]byte(args[0].String()), &p); err != nil {
			b, _ := json.Marshal(map[string]string{"error": "not an evidence pack: " + err.Error()})
			return string(b)
		}
		rep := evidence.Verify(p)
		b, _ := json.Marshal(map[string]any{"report": rep, "ruling": evidence.Rule(rep, args[1].String())})
		return string(b)
	}))
	js.Global().Set("warrantDecide", js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) < 1 {
			return `{"error":"warrantDecide(inputJSON)"}`
		}
		var in policy.Input
		if err := json.Unmarshal([]byte(args[0].String()), &in); err != nil {
			b, _ := json.Marshal(map[string]string{"error": "not a policy input: " + err.Error()})
			return string(b)
		}
		out := map[string]any{
			"caps_only": policy.Evaluate(in, policy.Options{CapsOnly: true}),
			"warrant":   policy.Evaluate(in, policy.Options{}),
		}
		if err := in.Scope.Validate(); err != nil {
			out["scope_error"] = err.Error()
		}
		b, _ := json.Marshal(out)
		return string(b)
	}))
	select {}
}
