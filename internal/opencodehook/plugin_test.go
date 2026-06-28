package opencodehook

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPluginSourceUsesUserMessageTurnKey(t *testing.T) {
	for _, want := range []string{
		`"chat.message"`,
		`activateUserTurn(input.sessionID, msg.id, "chat.message", true)`,
		`"event"`,
		`type === "message.updated"`,
		`activateUserTurn(sessionID, msg.id, "message.updated", false)`,
		`type === "session.idle"`,
		`clearActiveTurn(sessionID, "session.idle")`,
		`activeTurnBySession.get(input.sessionID)`,
		`"shell.env"`,
		`XIT_OPENCODE_TURN_KEY`,
		`opencodeTurnKey(sessionID, userMessageID)`,
		`isXitAutoCommand(cmd)`,
		`injectEnvIntoXitAuto(cmd)`,
	} {
		if !strings.Contains(PluginSource, want) {
			t.Fatalf("PluginSource missing %q", want)
		}
	}
}

func TestInstallWritesActivePluginWithOpenCodeIdentity(t *testing.T) {
	project := t.TempDir()
	home := filepath.Join(t.TempDir(), ".xit")
	if _, err := Install(project, home, false); err != nil {
		t.Fatalf("install failed: %v", err)
	}
	data, err := os.ReadFile(PluginPath(project))
	if err != nil {
		t.Fatalf("read active plugin: %v", err)
	}
	src := string(data)
	for _, want := range []string{
		`"chat.message"`,
		`"event"`,
		`role === "user"`,
		`msg.id`,
		`output.env.XIT_ADAPTER = "opencode"`,
		`XIT_OPENCODE_TURN_KEY`,
		`activeTurnBySession.get(input.sessionID)`,
		`activeTurnBySession.delete(sessionID)`,
		`tool.execute.before`,
		`output.args.command = finalCmd`,
		`injectEnvIntoXitAuto(cmd)`,
		`"shell.env"`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("active plugin missing %q", want)
		}
	}
	for _, bad := range []string{
		`XIT_OPENCODE_SESSION_ID`,
		`XIT_OPENCODE_USER_MESSAGE_ID`,
		`ses_`,
		`msg_`,
		`sessionID: input.sessionID`,
		`userMessageID:`,
	} {
		if strings.Contains(src, bad) {
			t.Fatalf("active plugin must not expose raw OpenCode IDs or old env names; found %q", bad)
		}
	}
}

func TestPluginSourceDoesNotUseCallIDAsTurnKey(t *testing.T) {
	if strings.Contains(PluginSource, "turnKey: input.callID") ||
		strings.Contains(PluginSource, "opencodeTurnKey(input.callID") {
		t.Fatal("callID must not be used as the OpenCode turn key")
	}
}

func TestPluginSourceRefreshesTurnKeyFromUserEventsAndIdle(t *testing.T) {
	for _, want := range []string{
		`function activateUserTurn(sessionID, userMessageID, source, allowReplace)`,
		`if (current && current.turnOpen && !allowReplace)`,
		`activeTurnBySession.set(sessionID, {`,
		`turnOpen: true`,
		`type === "message.updated"`,
		`activateUserTurn(sessionID, msg.id, "message.updated", false)`,
		`activeTurnBySession.delete(sessionID)`,
		`const activeTurn = activeTurnBySession.get(input.sessionID)`,
		`const turnKey = activeTurn && activeTurn.turnOpen ? activeTurn.turnKey : ""`,
	} {
		if !strings.Contains(PluginSource, want) {
			t.Fatalf("PluginSource missing turn-boundary guard %q", want)
		}
	}
	for _, bad := range []string{
		`const userMessageID = currentUserMessageBySession.get(input.sessionID)`,
		`opencodeTurnKey(input.sessionID, userMessageID)`,
	} {
		if strings.Contains(PluginSource, bad) {
			t.Fatalf("tool.execute.before must not recompute from stale user message cache; found %q", bad)
		}
	}
}

func TestPluginSourceUsesShellEnvAndNoTextCompleteFooter(t *testing.T) {
	for _, want := range []string{
		`"shell.env": async (input, output)`,
		`output.env.XIT_ADAPTER = "opencode"`,
		`output.env.XIT_OPENCODE_TURN_KEY = turnKey`,
	} {
		if !strings.Contains(PluginSource, want) {
			t.Fatalf("PluginSource missing shell.env feature %q", want)
		}
	}
	for _, bad := range []string{
		`"experimental.text.complete"`,
		`appendFooterIfNeeded`,
		`footer_emitted`,
		`XIT_ADAPTER=opencode XIT_OPENCODE_TURN_KEY=`,
		`function opencodeEnvPrefix`,
	} {
		if strings.Contains(PluginSource, bad) {
			t.Fatalf("OpenCode plugin must not retain final-answer footer or visible env-prefix code; found %q", bad)
		}
	}
}
