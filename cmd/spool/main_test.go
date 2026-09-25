package main

import "testing"

func TestParseCommand(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
		bad  bool
	}{
		{name: "default server", want: serverCommand},
		{name: "explicit server", args: []string{serverCommand}, want: serverCommand},
		{name: "worker", args: []string{workerCommand}, want: workerCommand},
		{name: "scheduler", args: []string{schedulerCommand}, want: schedulerCommand},
		{name: "migrate", args: []string{migrateCommand}, want: migrateCommand},
		{name: "unknown", args: []string{"unknown"}, bad: true},
		{name: "extra arguments", args: []string{workerCommand, "feed"}, bad: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseCommand(test.args)
			if test.bad {
				if err == nil {
					t.Fatal("parseCommand returned no error")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseCommand returned error: %v", err)
			}
			if got != test.want {
				t.Fatalf("parseCommand = %q, want %q", got, test.want)
			}
		})
	}
}
