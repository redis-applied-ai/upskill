package main

import (
	"os"
	"testing"
)

func TestMainFunction(t *testing.T) {
	oldArgs := os.Args
	oldExit := osExit
	defer func() {
		os.Args = oldArgs
		osExit = oldExit
	}()

	os.Args = []string{"upskill", "--help"}
	var got int
	osExit = func(code int) {
		got = code
		panic("exit")
	}
	defer func() {
		if recovered := recover(); recovered != "exit" {
			t.Fatalf("recovered = %#v", recovered)
		}
		if got != 0 {
			t.Fatalf("exit code = %d", got)
		}
	}()
	main()
}
