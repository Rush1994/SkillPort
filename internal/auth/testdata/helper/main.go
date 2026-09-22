// This standalone fixture implements only the credential-helper get protocol.
// It is test infrastructure, not Skill content.
package main

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"time"
)

func main() {
	if len(os.Args) != 2 || os.Args[1] != "get" {
		os.Exit(2)
	}
	b, _ := io.ReadAll(os.Stdin)
	switch strings.TrimSpace(string(b)) {
	case "missing.test":
		os.Stdout.WriteString("credentials not found in native keychain")
		os.Exit(1)
	case "failure.test":
		os.Stdout.WriteString("fixture-private-value")
		os.Exit(1)
	case "timeout.test":
		time.Sleep(10 * time.Second)
	case "token.test":
		json.NewEncoder(os.Stdout).Encode(map[string]string{"Username": "<token>", "Secret": "fixture-token"})
		return
	}
	json.NewEncoder(os.Stdout).Encode(map[string]string{"Username": "fixture-user", "Secret": "fixture-password"})
}
