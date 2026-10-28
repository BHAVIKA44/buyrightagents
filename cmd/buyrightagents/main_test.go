package main

import (
	"bytes"
	"os"
	"testing"
)

func TestRunHelp(t *testing.T) {
	oldOut, oldErr := os.Stdout, os.Stderr
	defer func() { os.Stdout, os.Stderr = oldOut, oldErr }()

	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout, os.Stderr = outW, errW
	code := run([]string{"help"})
	_ = outW.Close()
	_ = errW.Close()
	os.Stdout, os.Stderr = oldOut, oldErr

	var stdout, stderr bytes.Buffer
	_, _ = stdout.ReadFrom(outR)
	_, _ = stderr.ReadFrom(errR)
	if code != 0 || !bytes.Contains(stdout.Bytes(), []byte("BuyRightAgents")) || stderr.Len() != 0 {
		t.Fatalf("run(help) = %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
	}
}
