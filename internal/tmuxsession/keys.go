package tmuxsession

// restrictedBindings owns every reachable key table. In particular, neither
// application mouse events nor prefix keys can open a host command prompt,
// menu, window, or another session. This is UI policy, not a privilege boundary.
func restrictedBindings(binary, socket string) [][]string {
	commands := [][]string{
		{"unbind-key", "-a", "-T", "root"},
		{"unbind-key", "-a", "-T", "prefix"},
		{"unbind-key", "-a", "-T", "copy-mode"},
		{"unbind-key", "-a", "-T", "copy-mode-vi"},
		// Explain the available operations for unsupported prefix keys.
		{"bind-key", "-T", "prefix", "Any", "display-message", "-d", "1000", "Ctrl+B: D detach, [ history, ] paste, Ctrl+B send prefix"},
		{"bind-key", "-T", "prefix", "d", "detach-client"},
		{"bind-key", "-T", "prefix", "[", "copy-mode"},
		{"bind-key", "-T", "prefix", "C-b", "send-prefix"},
		{"bind-key", "-T", "prefix", "]", "paste-buffer", "-p"},
		{"bind-key", "-T", "root", "MouseDown1Pane", "send-keys", "-M"},
		{"bind-key", "-T", "root", "MouseUp1Pane", "send-keys", "-M"},
		{"bind-key", "-T", "root", "WheelUpPane", "if-shell", "-F", "#{||:#{pane_in_mode},#{mouse_any_flag}}", "send-keys -M", "copy-mode -e; send-keys -X -N 5 scroll-up"},
		{"bind-key", "-T", "root", "WheelDownPane", "send-keys", "-M"},
	}
	for _, table := range []string{"copy-mode", "copy-mode-vi"} {
		for _, binding := range [][2]string{
			{"Escape", "cancel"}, {"q", "cancel"}, {"C-c", "cancel"},
			{"Left", "cursor-left"}, {"Right", "cursor-right"},
			{"Up", "cursor-up"}, {"Down", "cursor-down"},
			{"Home", "start-of-line"}, {"End", "end-of-line"},
			{"PageUp", "page-up"}, {"PageDown", "page-down"},
			{"C-Home", "history-top"}, {"C-End", "history-bottom"},
			{"Space", "begin-selection"}, {"MouseDown1Pane", "begin-selection"},
			{"MouseDrag1Pane", "begin-selection"},
		} {
			commands = append(commands, []string{"bind-key", "-T", table, binding[0], "send-keys", "-X", binding[1]})
		}
		for _, binding := range [][2]string{{"WheelUpPane", "scroll-up"}, {"WheelDownPane", "scroll-down"}} {
			commands = append(commands, []string{"bind-key", "-T", table, binding[0], "send-keys", "-X", "-N", "5", binding[1]})
		}
		for _, key := range []string{"Enter", "y"} {
			commands = append(commands, []string{"bind-key", "-T", table, key, "run-shell", "-b", copySelectionCommand(binary, socket)})
		}
	}
	for _, binding := range [][2]string{
		{"h", "cursor-left"}, {"j", "cursor-down"}, {"k", "cursor-up"}, {"l", "cursor-right"},
		{"g", "history-top"}, {"G", "history-bottom"}, {"v", "begin-selection"},
	} {
		commands = append(commands, []string{"bind-key", "-T", "copy-mode-vi", binding[0], "send-keys", "-X", binding[1]})
	}
	return commands
}
