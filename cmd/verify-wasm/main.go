//go:build js && wasm

// Command verify-wasm is the evidence checker built for the browser. The
// website loads it to check a pack on the visitor's machine with the same
// code as warrant verify, policy included.
package main

import (
	"encoding/json"
	"syscall/js"

	"github.com/Rakshit-gen/warrant/evidence"
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
	select {}
}
