package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/plugins/reference/runtime"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) != 2 {
		return errors.New("one protocol command is required")
	}
	input, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
	if err != nil {
		return err
	}
	plugin := reference.Plugin{}
	log := func(_ string, message string) error {
		fmt.Fprintln(os.Stderr, message)
		return nil
	}
	var output any
	switch os.Args[1] {
	case "describe":
		output = plugin.Manifest()
	case "validate":
		output = plugin.Validate(context.Background(), input)
	case "plan":
		output, err = plugin.Plan(context.Background(), input)
	case "precheck":
		var request stepRequest
		err = json.Unmarshal(input, &request)
		if err == nil {
			output, err = plugin.Precheck(context.Background(), request.Step, log)
		}
	case "execute":
		var request stepRequest
		err = json.Unmarshal(input, &request)
		if err == nil {
			output, err = plugin.Execute(context.Background(), request.Step, log)
		}
	case "verify":
		var request verifyRequest
		err = json.Unmarshal(input, &request)
		if err == nil {
			output, err = plugin.Verify(context.Background(), request.Step, request.Result, log)
		}
	case "status":
		output = map[string]string{"status": "idle"}
	case "cancel", "cleanup":
		output = map[string]string{"status": "accepted"}
	default:
		return fmt.Errorf("unsupported command %q", os.Args[1])
	}
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(output)
}

type stepRequest struct {
	Step domain.PlanStep `json:"step"`
}

type verifyRequest struct {
	Step   domain.PlanStep `json:"step"`
	Result json.RawMessage `json:"result"`
}
