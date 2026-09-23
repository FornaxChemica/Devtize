package golang

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	devprocess "github.com/FornaxChemica/devtize/internal/process"
)

type fakeRunner struct {
	specs   []devprocess.CommandSpec
	results []devprocess.CommandResult
	errors  []error
}

func (r *fakeRunner) Run(_ context.Context, spec devprocess.CommandSpec) (devprocess.CommandResult, error) {
	r.specs = append(r.specs, spec)
	index := len(r.specs) - 1
	var result devprocess.CommandResult
	var err error
	if index < len(r.results) {
		result = r.results[index]
	}
	if index < len(r.errors) {
		err = r.errors[index]
	}
	return result, err
}

func TestDetectAndRunUseExactReviewedCommands(t *testing.T) {
	runner := &fakeRunner{results: []devprocess.CommandResult{{Stdout: "go version go1.27.0 darwin/arm64\n"}, {}, {}, {}}}
	adapter := Adapter{
		Runner: runner, LookPath: func(name string) (string, error) { return "/tools/" + name, nil },
		Now: func() time.Time { return time.Unix(0, 0) },
	}
	toolchain, err := adapter.Detect(context.Background(), "/work")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"go.test", "go.vet", "go.build"} {
		if _, err := adapter.Run(context.Background(), CheckInput{CapabilityID: id, ProjectRoot: "/work", Toolchain: toolchain}); err != nil {
			t.Fatal(err)
		}
	}
	want := [][]string{{"version"}, {"test", "./..."}, {"vet", "./..."}, {"build", "./..."}}
	for index, spec := range runner.specs {
		if !reflect.DeepEqual(spec.Args, want[index]) || spec.Dir != "/work" || spec.Stdin != devprocess.StdinDisabled || spec.CaptureLimit != 1<<20 {
			t.Fatalf("spec %d = %#v", index, spec)
		}
	}
	if runner.specs[1].Timeout != 10*time.Minute || runner.specs[2].Timeout != 5*time.Minute || runner.specs[3].Timeout != 5*time.Minute {
		t.Fatalf("unexpected timeouts: %#v", runner.specs)
	}
}

func TestFormatPreservesHostilePathsAsLiteralArguments(t *testing.T) {
	runner := &fakeRunner{results: []devprocess.CommandResult{{}}}
	adapter := Adapter{Runner: runner, Now: func() time.Time { return time.Unix(0, 0) }}
	paths := []string{"normal.go", "dir/a b;$HOME.go"}
	_, err := adapter.Run(context.Background(), CheckInput{
		CapabilityID: "go.format.check", ProjectRoot: "/work", GoFiles: paths,
		Toolchain: Toolchain{GofmtPath: "/tools/gofmt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-l", "./normal.go", "./dir/a b;$HOME.go"}
	if len(runner.specs) != 1 || !reflect.DeepEqual(runner.specs[0].Args, want) {
		t.Fatalf("args = %#v, want %#v", runner.specs[0].Args, want)
	}
}

func TestFormatBatchesAndBoundsFileInventory(t *testing.T) {
	files := make([]string, formatBatch+1)
	for index := range files {
		files[index] = fmt.Sprintf("file-%03d.go", index)
	}
	runner := &fakeRunner{results: []devprocess.CommandResult{{}, {}}}
	adapter := Adapter{Runner: runner, Now: func() time.Time { return time.Unix(0, 0) }}
	_, err := adapter.Run(context.Background(), CheckInput{
		CapabilityID: "go.format.check", ProjectRoot: "/work", GoFiles: files,
		Toolchain: Toolchain{GofmtPath: "/tools/gofmt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(runner.specs) != 2 || len(runner.specs[0].Args) != formatBatch+1 || len(runner.specs[1].Args) != 2 {
		t.Fatalf("unexpected format batches: %#v", runner.specs)
	}

	overLimit := make([]string, maxGoFiles+1)
	_, err = adapter.Run(context.Background(), CheckInput{
		CapabilityID: "go.format.check", ProjectRoot: "/work", GoFiles: overLimit,
		Toolchain: Toolchain{GofmtPath: "/tools/gofmt"},
	})
	if !errors.Is(err, ErrCheckFailed) || len(runner.specs) != 2 {
		t.Fatalf("over-limit result: calls=%d err=%v", len(runner.specs), err)
	}
}

func TestFormatOutputAndProcessFailuresAreCheckFailures(t *testing.T) {
	runner := &fakeRunner{results: []devprocess.CommandResult{{Stdout: "bad.go\n"}}}
	adapter := Adapter{Runner: runner}
	result, err := adapter.Run(context.Background(), CheckInput{
		CapabilityID: "go.format.check", ProjectRoot: "/work", GoFiles: []string{"bad.go"}, Toolchain: Toolchain{GofmtPath: "gofmt"},
	})
	if !errors.Is(err, ErrCheckFailed) || !strings.Contains(result.Diagnostic, "bad.go") {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}
