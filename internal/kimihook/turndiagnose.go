package kimihook

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// TurnDiagnoseResult holds the turn diagnose output.
type TurnDiagnoseResult struct {
	ProjectState  TurnStateCheck   `json:"project_state"`
	UserState     TurnStateCheck   `json:"user_state"`
	EventsLog     EventsLogCheck   `json:"events_log"`
	HookConfig    HookConfigCheck  `json:"hook_config"`
	Scripts       ScriptsCheck     `json:"scripts"`
	Diagnosis     []string         `json:"diagnosis"`
}

type TurnStateCheck struct {
	Path   string      `json:"path"`
	Exists bool        `json:"exists"`
	Status string      `json:"status"`
	Event  string      `json:"event"`
	Age    string      `json:"age"`
}

type EventsLogCheck struct {
	Path         string        `json:"path"`
	Exists       bool          `json:"exists"`
	LatestEvents []LogEventRec `json:"latest_events"`
}

type LogEventRec struct {
	Event     string `json:"event"`
	Status    string `json:"status"`
	StateFile string `json:"state_file"`
	Time      string `json:"time"`
}

type HookConfigCheck struct {
	UserPromptSubmit bool `json:"UserPromptSubmit"`
	Stop             bool `json:"Stop"`
	SessionStart     bool `json:"SessionStart"`
	SessionEnd       bool `json:"SessionEnd"`
}

type ScriptsCheck struct {
	UserPromptSubmit string `json:"UserPromptSubmit"`
	Stop             string `json:"Stop"`
	SessionStart     string `json:"SessionStart"`
	SessionEnd       string `json:"SessionEnd"`
}

// RunTurnDiagnose performs a live diagnosis of the Kimi turn lifecycle system.
func RunTurnDiagnose(home string, useJSON bool) {
	projectHome, userHome := ResolveTurnStateHome("")
	res := &TurnDiagnoseResult{}

	// Project state
	projectStatePath := filepath.Join(projectHome, "state", "turn.json")
	res.ProjectState.Path = projectStatePath
	if data, err := os.ReadFile(projectStatePath); err == nil {
		res.ProjectState.Exists = true
		var s TurnState
		_ = json.Unmarshal(data, &s)
		res.ProjectState.Status = s.Status
		res.ProjectState.Event = s.Event
		res.ProjectState.Age = computeAge(s.StartedAt, s.FinishedAt)
	}

	// User state
	userStatePath := filepath.Join(userHome, "state", "turn.json")
	res.UserState.Path = userStatePath
	if data, err := os.ReadFile(userStatePath); err == nil {
		res.UserState.Exists = true
		var s TurnState
		_ = json.Unmarshal(data, &s)
		res.UserState.Status = s.Status
		res.UserState.Event = s.Event
		res.UserState.Age = computeAge(s.StartedAt, s.FinishedAt)
	}

	// Events log
	logPath := filepath.Join(userHome, "kimi-hooks", "turn-events.jsonl")
	res.EventsLog.Path = logPath
	if data, err := os.ReadFile(logPath); err == nil {
		res.EventsLog.Exists = true
		lines := splitLines(string(data))
		// Collect last 5 events in reverse order.
		count := 0
		for i := len(lines) - 1; i >= 0 && count < 5; i-- {
			line := trimSpace(lines[i])
			if line == "" {
				continue
			}
			var rec struct {
				Event     string `json:"event"`
				Status    string `json:"status"`
				StateFile string `json:"state_file"`
				Time      string `json:"time"`
			}
			if err := json.Unmarshal([]byte(line), &rec); err == nil {
				res.EventsLog.LatestEvents = append(res.EventsLog.LatestEvents, LogEventRec{
					Event:     rec.Event,
					Status:    rec.Status,
					StateFile: rec.StateFile,
					Time:      rec.Time,
				})
				count++
			}
		}
	}

	// Hook config (check user-scope by default)
	configPath := UserConfigPath()
	if _, err := os.Stat(configPath); err != nil {
		configPath = ProjectConfigPath()
	}
	format := DetectConfigFormat(configPath)
	res.HookConfig = HookConfigCheck{}
	switch format {
	case FormatTOML:
		content, _ := ReadToml(configPath)
		res.HookConfig.UserPromptSubmit = HasXiTEventToml(content, "UserPromptSubmit")
		res.HookConfig.Stop = HasXiTEventToml(content, "Stop")
		res.HookConfig.SessionStart = HasXiTEventToml(content, "SessionStart")
		res.HookConfig.SessionEnd = HasXiTEventToml(content, "SessionEnd")
	case FormatJSON:
		settings, _ := ReadSettings(configPath)
		scriptPath := filepath.Join(home, "hooks", "kimi-pretooluse-shell.sh")
		res.HookConfig.UserPromptSubmit = HasXiTEvent(settings, "UserPromptSubmit", scriptPath)
		res.HookConfig.Stop = HasXiTEvent(settings, "Stop", scriptPath)
		res.HookConfig.SessionStart = HasXiTEvent(settings, "SessionStart", scriptPath)
		res.HookConfig.SessionEnd = HasXiTEvent(settings, "SessionEnd", scriptPath)
	}

	// Scripts
	for _, event := range []string{"UserPromptSubmit", "Stop", "SessionStart", "SessionEnd"} {
		path := filepath.Join(home, "hooks", turnLifecycleScriptName(event))
		status := "missing"
		if info, err := os.Stat(path); err == nil {
			if info.Mode()&0111 != 0 {
				status = "exists/executable"
			} else {
				status = "exists/not-executable"
			}
		}
		switch event {
		case "UserPromptSubmit":
			res.Scripts.UserPromptSubmit = status
		case "Stop":
			res.Scripts.Stop = status
		case "SessionStart":
			res.Scripts.SessionStart = status
		case "SessionEnd":
			res.Scripts.SessionEnd = status
		}
	}

	// Diagnosis
	var diagnosis []string
	if res.EventsLog.Exists && !res.ProjectState.Exists {
		diagnosis = append(diagnosis, "state path mismatch: events logged but project state missing")
	}
	if res.EventsLog.Exists && len(res.EventsLog.LatestEvents) > 0 {
		latest := res.EventsLog.LatestEvents[0]
		if latest.Event == "" {
			diagnosis = append(diagnosis, "event identity lost: latest event has empty event name")
		}
	}
	if !res.HookConfig.UserPromptSubmit && !res.HookConfig.Stop && !res.HookConfig.SessionStart && !res.HookConfig.SessionEnd {
		diagnosis = append(diagnosis, "hook config missing: reinstall hooks with xit init kimi --method official_hook --scope user --yes")
	}
	if res.ProjectState.Exists || res.UserState.Exists {
		// State updates are happening.
		if res.ProjectState.Exists && res.ProjectState.Status == "" && res.UserState.Exists && res.UserState.Status == "" {
			diagnosis = append(diagnosis, "state files exist but both empty: possible write failure")
		}
	}
	if len(diagnosis) == 0 {
		diagnosis = append(diagnosis, "no obvious issues detected")
	}
	res.Diagnosis = diagnosis

	if useJSON {
		data, _ := json.MarshalIndent(res, "", "  ")
		fmt.Println(string(data))
		return
	}

	fmt.Println("XiT Kimi Turn Diagnose")
	fmt.Println()
	fmt.Println("project_state:")
	fmt.Printf("  path:   %s\n", res.ProjectState.Path)
	fmt.Printf("  exists: %v\n", boolToYesNo(res.ProjectState.Exists))
	if res.ProjectState.Exists {
		fmt.Printf("  status: %s\n", res.ProjectState.Status)
		fmt.Printf("  event:  %s\n", res.ProjectState.Event)
		fmt.Printf("  age:    %s\n", res.ProjectState.Age)
	}
	fmt.Println()
	fmt.Println("user_state:")
	fmt.Printf("  path:   %s\n", res.UserState.Path)
	fmt.Printf("  exists: %v\n", boolToYesNo(res.UserState.Exists))
	if res.UserState.Exists {
		fmt.Printf("  status: %s\n", res.UserState.Status)
		fmt.Printf("  event:  %s\n", res.UserState.Event)
		fmt.Printf("  age:    %s\n", res.UserState.Age)
	}
	fmt.Println()
	fmt.Println("events_log:")
	fmt.Printf("  path:   %s\n", res.EventsLog.Path)
	fmt.Printf("  exists: %v\n", boolToYesNo(res.EventsLog.Exists))
	if len(res.EventsLog.LatestEvents) > 0 {
		fmt.Println("  latest_events:")
		for _, ev := range res.EventsLog.LatestEvents {
			fmt.Printf("    - event: %s, status: %s, state_file: %s, time: %s\n", ev.Event, ev.Status, ev.StateFile, ev.Time)
		}
	}
	fmt.Println()
	fmt.Println("hook_config:")
	fmt.Printf("  UserPromptSubmit: %s\n", boolToYesNo(res.HookConfig.UserPromptSubmit))
	fmt.Printf("  Stop:             %s\n", boolToYesNo(res.HookConfig.Stop))
	fmt.Printf("  SessionStart:     %s\n", boolToYesNo(res.HookConfig.SessionStart))
	fmt.Printf("  SessionEnd:       %s\n", boolToYesNo(res.HookConfig.SessionEnd))
	fmt.Println()
	fmt.Println("scripts:")
	fmt.Printf("  kimi-turn-userpromptsubmit.sh: %s\n", res.Scripts.UserPromptSubmit)
	fmt.Printf("  kimi-turn-stop.sh:             %s\n", res.Scripts.Stop)
	fmt.Printf("  kimi-turn-sessionstart.sh:     %s\n", res.Scripts.SessionStart)
	fmt.Printf("  kimi-turn-sessionend.sh:       %s\n", res.Scripts.SessionEnd)
	fmt.Println()
	fmt.Println("diagnosis:")
	for _, d := range res.Diagnosis {
		fmt.Printf("  - %s\n", d)
	}
}

func computeAge(startedAt, finishedAt string) string {
	ref := startedAt
	if ref == "" {
		ref = finishedAt
	}
	if ref == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, ref)
	if err != nil {
		return ""
	}
	return time.Since(t).Round(time.Second).String()
}

func boolToYesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}
