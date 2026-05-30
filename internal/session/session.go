package session

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/stephenywilson/xit/internal/history"
	"github.com/stephenywilson/xit/internal/util"
)

type Session struct {
	ID        string    `json:"id"`
	Command   string    `json:"command"`
	StartTime time.Time `json:"start_time"`
	Dir       string    `json:"dir"`
}

type Meta struct {
	ID        string `json:"id"`
	Command   string `json:"command"`
	StartTime string `json:"start_time"`
}

type Stats struct {
	Commands            int
	RawBytes            int
	SummaryBytes        int
	EstimatedReduction  float64
	EstimatedSavedBytes int
	EstimatedSavedTokens int
	RawLogBytes         int64
	RawLogPath          string
	HistoryPath         string
}

// New creates a session directory and writes meta.json.
// It uses millisecond precision and retries if a directory collision occurs.
func New(home string, command []string) (*Session, error) {
	slug := util.CommandSlug(command)
	var id, dir string
	for attempt := 0; attempt < 10; attempt++ {
		ts := util.TimestampSlug()
		if attempt > 0 {
			id = fmt.Sprintf("%s-%s-%d", ts, slug, attempt)
		} else {
			id = fmt.Sprintf("%s-%s", ts, slug)
		}
		dir = filepath.Join(home, "sessions", id)
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			break
		}
		if attempt == 9 {
			return nil, fmt.Errorf("could not create unique session directory after 10 attempts")
		}
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}

	s := &Session{
		ID:        id,
		Command:   strings.Join(command, " "),
		StartTime: time.Now(),
		Dir:       dir,
	}

	meta := Meta{
		ID:        s.ID,
		Command:   s.Command,
		StartTime: s.StartTime.Format(time.RFC3339),
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), append(data, '\n'), 0644); err != nil {
		return nil, err
	}

	// Touch history.jsonl and raw.log so they exist.
	os.WriteFile(filepath.Join(dir, "history.jsonl"), []byte{}, 0644)
	os.WriteFile(filepath.Join(dir, "raw.log"), []byte{}, 0644)

	return s, nil
}

// Environ returns environment variables to inject into the child process.
func (s *Session) Environ() []string {
	return []string{
		fmt.Sprintf("XIT_SESSION_ID=%s", s.ID),
		fmt.Sprintf("XIT_SESSION_DIR=%s", s.Dir),
	}
}

// StartBanner returns a session start banner in the given mode.
func (s *Session) StartBanner(mode string) string {
	switch mode {
	case "agent":
		return fmt.Sprintf(
			"[xit:session start id=%s target=%s mode=agent raw_log=%s]\npolicy=explicit_xit_commands_are_summarized full_session_output_is_preserved\n",
			s.ID, s.Command, filepath.Join(s.Dir, "raw.log"),
		)
	case "json":
		data := map[string]interface{}{
			"event":      "session_start",
			"session_id": s.ID,
			"target":     s.Command,
			"mode":       "json",
			"raw_log":    filepath.Join(s.Dir, "raw.log"),
			"policy":     "explicit xit commands are summarized; full session output is preserved",
		}
		b, _ := json.Marshal(data)
		return string(b) + "\n"
	default:
		var b strings.Builder
		b.WriteString("\n")
		b.WriteString("╔══════════════════════════════════════════════════════════╗\n")
		b.WriteString("║  XiT Session Mode                                        ║\n")
		b.WriteString(fmt.Sprintf("║  Session ID : %s\n", s.ID))
		b.WriteString(fmt.Sprintf("║  Command    : %s\n", s.Command))
		b.WriteString(fmt.Sprintf("║  Start      : %s\n", s.StartTime.Format("15:04:05")))
		b.WriteString("╚══════════════════════════════════════════════════════════╝\n")
		b.WriteString("\n")
		return b.String()
	}
}

// computeStats reads the session history.jsonl and raw.log to compute aggregates.
func (s *Session) computeStats() Stats {
	st := Stats{
		RawLogPath:  filepath.Join(s.Dir, "raw.log"),
		HistoryPath: filepath.Join(s.Dir, "history.jsonl"),
	}

	// Raw log size.
	if info, err := os.Stat(st.RawLogPath); err == nil {
		st.RawLogBytes = info.Size()
	}

	// Parse history.jsonl.
	f, err := os.Open(st.HistoryPath)
	if err != nil {
		return st
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var r history.Record
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			continue
		}
		st.Commands++
		st.RawBytes += r.RawBytes
		st.SummaryBytes += r.SummaryBytes
	}

	if st.RawBytes > 0 {
		st.EstimatedReduction = 1.0 - float64(st.SummaryBytes)/float64(st.RawBytes)
		if st.EstimatedReduction < 0 {
			st.EstimatedReduction = 0
		}
		st.EstimatedSavedBytes = st.RawBytes - st.SummaryBytes
		if st.EstimatedSavedBytes < 0 {
			st.EstimatedSavedBytes = 0
		}
		st.EstimatedSavedTokens = st.EstimatedSavedBytes / 4
	}
	return st
}

// EndReport returns a session summary after the child exits.
func (s *Session) EndReport(mode string, exitCode int) string {
	duration := time.Since(s.StartTime)
	st := s.computeStats()

	switch mode {
	case "agent":
		return fmt.Sprintf(
			"[xit:session end id=%s target=%s exit_code=%d duration_ms=%d commands=%d raw_bytes=%d summary_bytes=%d reduction=%.1f saved_tokens_est=%d raw_log=%s history=%s]\n",
			s.ID, s.Command, exitCode, duration.Milliseconds(), st.Commands, st.RawBytes, st.SummaryBytes,
			st.EstimatedReduction*100, st.EstimatedSavedTokens, st.RawLogPath, st.HistoryPath,
		)
	case "json":
		data := map[string]interface{}{
			"event":                  "session_end",
			"session_id":             s.ID,
			"target":                 s.Command,
			"exit_code":              exitCode,
			"duration_ms":            duration.Milliseconds(),
			"commands":               st.Commands,
			"raw_bytes":              st.RawBytes,
			"summary_bytes":          st.SummaryBytes,
			"estimated_reduction":    st.EstimatedReduction,
			"estimated_saved_tokens": st.EstimatedSavedTokens,
			"raw_log":                st.RawLogPath,
			"history":                st.HistoryPath,
		}
		b, _ := json.Marshal(data)
		return string(b) + "\n"
	default:
		var b strings.Builder
		b.WriteString("\n")
		b.WriteString("╔══════════════════════════════════════════════════════════╗\n")
		b.WriteString("║  吸T神功本轮战报                                         ║\n")
		b.WriteString(fmt.Sprintf("║  session    : %s\n", s.ID))
		b.WriteString(fmt.Sprintf("║  target     : %s\n", s.Command))
		b.WriteString(fmt.Sprintf("║  duration   : %s\n", duration.Round(time.Second)))
		b.WriteString(fmt.Sprintf("║  exit_code  : %d\n", exitCode))
		b.WriteString(fmt.Sprintf("║  commands   : %d\n", st.Commands))
		b.WriteString(fmt.Sprintf("║  raw output : %s\n", humanBytes(st.RawBytes)))
		b.WriteString(fmt.Sprintf("║  compressed : %s\n", humanBytes(st.SummaryBytes)))
		b.WriteString(fmt.Sprintf("║  reduction  : %.1f%%\n", st.EstimatedReduction*100))
		b.WriteString(fmt.Sprintf("║  saved      : ~%d estimated tokens\n", st.EstimatedSavedTokens))
		b.WriteString(fmt.Sprintf("║  session log: %s\n", st.RawLogPath))
		b.WriteString(fmt.Sprintf("║  records    : %s\n", st.HistoryPath))
		b.WriteString("╚══════════════════════════════════════════════════════════╝\n")
		b.WriteString("\n")
		if st.Commands == 0 {
			b.WriteString("note: no explicit xit-wrapped commands were recorded in this session\n")
			b.WriteString("      run `xit --mode agent <command>` inside the session to record them\n")
			b.WriteString("\n")
		}
		return b.String()
	}
}

func humanBytes(n int) string {
	if n >= 1024*1024 {
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	}
	if n >= 1024 {
		return fmt.Sprintf("%.1f kB", float64(n)/1024)
	}
	return fmt.Sprintf("%d B", n)
}

// AppendHistory writes a history record to the session's history.jsonl.
func (s *Session) AppendHistory(r history.Record) error {
	path := filepath.Join(s.Dir, "history.jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(f, string(data))
	return err
}
