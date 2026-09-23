package ui

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"unicode/utf8"

	golangadapter "github.com/FornaxChemica/devtize/internal/adapters/golang"
	"github.com/FornaxChemica/devtize/internal/app"
	"github.com/FornaxChemica/devtize/internal/config"
	"github.com/FornaxChemica/devtize/internal/operation"
	"golang.org/x/term"
)

type Capabilities struct {
	TTY     bool
	Width   int
	Unicode bool
}

type Options struct {
	Color        config.ColorMode
	Environment  map[string]string
	Capabilities *Capabilities
}

type Renderer struct {
	w       io.Writer
	width   int
	color   bool
	unicode bool
}

func New(writer io.Writer, options Options) *Renderer {
	capabilities := detectCapabilities(writer, options.Environment)
	if options.Capabilities != nil {
		capabilities = *options.Capabilities
	}
	width := capabilities.Width
	if width == 0 {
		width = 100
	}
	if width < 40 {
		width = 40
	}
	if width > 120 {
		width = 120
	}
	color := options.Color == config.ColorAlways || options.Color == config.ColorAuto && capabilities.TTY
	if options.Color == config.ColorNever || options.Environment["NO_COLOR"] != "" || options.Environment["TERM"] == "dumb" {
		color = false
	}
	return &Renderer{w: writer, width: width, color: color, unicode: capabilities.Unicode}
}

func detectCapabilities(writer io.Writer, environment map[string]string) Capabilities {
	capabilities := Capabilities{Width: 100}
	file, ok := writer.(*os.File)
	if !ok || !term.IsTerminal(int(file.Fd())) || environment["TERM"] == "dumb" {
		return capabilities
	}
	capabilities.TTY = true
	if width, _, err := term.GetSize(int(file.Fd())); err == nil {
		capabilities.Width = width
	}
	locale := environment["LC_ALL"] + environment["LC_CTYPE"] + environment["LANG"]
	capabilities.Unicode = strings.Contains(strings.ToUpper(locale), "UTF-8") || strings.Contains(strings.ToUpper(locale), "UTF8")
	return capabilities
}

func (r *Renderer) Doctor(response app.DoctorResponse) {
	r.heading("Devtize doctor", response.Status)
	r.field("config", response.Config.Status)
	if response.Project.Status == "detected" {
		r.field("project", fmt.Sprintf("detected (%s, confidence %s, ambiguous %t)", response.Project.Root, response.Project.Confidence, response.Project.Ambiguous))
	} else {
		r.field("project", "not found")
	}
	for _, tool := range response.Tools {
		value := string(tool.Status)
		if tool.Version != "" {
			value += " " + tool.Version
		}
		if tool.AuthStatus != "" {
			value += "; auth " + tool.AuthStatus
		}
		r.field(tool.ProviderID, value)
	}
	r.field("registry", fmt.Sprintf("%s (%s; %d reviewed builtin entries)", response.Registry.Status, response.Registry.KnowledgeStatus, response.Registry.Entries))
}

func (r *Renderer) Find(response app.FindResponse) {
	for index, result := range response.Results {
		if index > 0 {
			fmt.Fprintln(r.w)
		}
		fmt.Fprintln(r.w, r.strong(result.Command))
		r.field("summary", result.Summary)
		r.field("source", result.Source.Kind)
		r.field("risk", string(result.Risk))
		r.field("match", result.MatchReason+" ("+string(result.Confidence)+")")
		r.field("versions", result.VersionRange+" ("+result.VersionStatus+")")
		r.field("effect", strings.Join(result.Effects, "; "))
	}
}

func (r *Renderer) Version(info app.BuildInfo) {
	fmt.Fprintf(r.w, "%s %s (%s, commit %s, built %s)\n", info.Product, info.Version, info.Command, info.Commit, info.BuiltAt)
}

func (r *Renderer) Error(err error) {
	var operational *app.Error
	if !errors.As(err, &operational) {
		operational = app.Wrap(app.CodeProcessFailed, "command failed", err)
	}
	r.heading("Error", string(operational.Code))
	r.field("message", operational.Message)
	if operational.Provider != "" {
		r.field("provider", operational.Provider)
	}
	if operational.Hint != "" {
		r.field("next", operational.Hint)
	}
}

func (r *Renderer) RepoPlan(response app.RepoResponse) {
	r.planHeader(response.Plan)
	r.paths("selected files ("+response.Selection.Mode+")", response.Selection.Paths)
	if len(response.Selection.UntrackPaths) > 0 {
		r.paths(fmt.Sprintf("tracked ignored paths to remove from the commit (%d)", len(response.Selection.UntrackPaths)), response.Selection.UntrackPaths)
	}
	for _, warning := range response.Warnings {
		r.warning(warning)
	}
	r.operations(response.Plan.Operations)
}

func (r *Renderer) RepoDescriptionPlan(response app.RepoDescriptionResponse) {
	r.planHeader(response.Plan)
	r.field("repository", response.GitHubRepo.NameWithOwner)
	r.field("current description", response.GitHubRepo.Description)
	if len(response.Plan.Operations) == 0 {
		r.field("operations", "none (description already matches)")
		return
	}
	r.field("new description", fmt.Sprint(response.Plan.Operations[0].Inputs["description"]))
	r.operations(response.Plan.Operations)
}

func (r *Renderer) CommitPlan(response app.CommitResponse) {
	r.planHeader(response.Plan)
	r.field("branch", response.Git.HeadBranch)
	r.field("HEAD", response.Git.HeadCommit)
	if op, ok := findOperation(response.Plan, "git.commit.create"); ok {
		r.field("message", fmt.Sprint(op.Inputs["message"]))
	}
	r.paths("selected files ("+response.Selection.Mode+")", response.Selection.Paths)
	for _, warning := range response.Selection.Warnings {
		r.warning(warning)
	}
	r.operations(response.Plan.Operations)
}

func (r *Renderer) ShipPlan(response app.ShipResponse) {
	r.planHeader(response.Plan)
	r.field("remote", fmt.Sprintf("%s (%s)", response.RemoteName, response.RemoteURL))
	r.field("branch", response.Git.HeadBranch)
	r.field("live remote", response.RemoteCommit)
	r.field("local HEAD", response.Git.HeadCommit)
	if len(response.Commits) == 0 {
		r.field("outgoing commits", "none")
	} else {
		fmt.Fprintf(r.w, "%s (%d):\n", r.label("outgoing commits"), len(response.Commits))
		for _, commit := range response.Commits {
			r.bullet(commit.SHA + " " + commit.Subject)
		}
	}
	if len(response.ExcludedPaths) == 0 {
		r.field("excluded working changes", "none")
	} else {
		r.paths("excluded working changes", response.ExcludedPaths)
	}
	if len(response.Plan.Operations) == 0 {
		r.field("operations", "none (remote already matches local HEAD)")
		return
	}
	r.operations(response.Plan.Operations)
}

func (r *Renderer) ShipWorkflowPlan(response app.ShipWorkflowResponse) {
	r.heading("Composed ship", response.WorkflowID)
	r.planHeader(response.Local.Plan)
	r.field("branch", response.Local.Git.HeadBranch)
	r.field("HEAD", response.Local.Git.HeadCommit)
	r.field("remote", fmt.Sprintf("%s (%s)", response.Remote.RemoteName, response.Remote.RemoteURL))
	r.field("live remote", response.Remote.RemoteCommit)
	if op, ok := findOperation(response.Local.Plan, "git.commit.create"); ok {
		r.field("message", fmt.Sprint(op.Inputs["message"]))
	}
	r.paths("selected files ("+response.Local.Selection.Mode+")", response.Local.Selection.Paths)
	if countChecks(response.Local.Plan) == 0 {
		r.warning("no checks configured")
	}
	for _, warning := range response.Local.Selection.Warnings {
		r.warning(warning)
	}
	r.operations(response.Local.Plan.Operations)
	r.field("push", "deferred until the created commit has an exact SHA; a separate push plan and confirmation will follow")
}

func (r *Renderer) Result(result operation.ExecutionResult) {
	r.field("result", string(result.Status))
	for _, step := range result.Steps {
		r.bullet(step.CapabilityID + ": " + string(step.Status))
		if step.RecoveryHint != "" {
			r.indentedField("recovery", step.RecoveryHint)
		}
	}
}

func (r *Renderer) RepoStatus(response app.RepoResponse) {
	if !response.Git.IsRepository {
		r.field("repository", "not initialized")
		return
	}
	r.heading("Repository", "initialized")
	r.field("branch", response.Git.HeadBranch)
	r.field("commit", response.Git.HeadCommit)
	r.field("working tree", response.Git.WorkingTreeStatus)
	r.field("upstream", fallback(response.Git.Upstream, "not configured"))
	r.field("origin", fallback(strings.Join(response.Git.RemoteURLs, ", "), "not configured"))
}

func (r *Renderer) Status(response app.StatusResponse) {
	r.heading("Repository status", response.Repository.WorkingTree)
	r.field("project", response.ProjectRoot)
	if !response.Repository.IsRepository {
		r.field("repository", "not initialized")
	} else {
		r.field("repository", "initialized")
		r.field("branch", fallback(response.Repository.Branch, "(detached or unborn)"))
		r.field("HEAD", fallback(response.Repository.HeadCommit, "none"))
		r.field("working tree", response.Repository.WorkingTree)
	}
	r.field("upstream", fallback(response.Upstream.Name, "not configured"))
	r.field("relation", fmt.Sprintf("%s (ahead %d, behind %d)", response.Upstream.Relation, response.Upstream.Ahead, response.Upstream.Behind))
	r.field("remote "+response.LiveRemote.Name, fallback(strings.Join(response.LiveRemote.URLs, ", "), "not configured"))
	r.statusPaths("staged", response.Changes.Staged)
	r.statusPaths("unstaged", response.Changes.Unstaged)
	r.statusPaths("untracked", response.Changes.Untracked)
	r.field("ignored paths", fmt.Sprint(response.Changes.IgnoredCount))
	if response.LiveRemote.Requested {
		r.field("live remote", response.LiveRemote.Name)
		r.field("live relation", fallback(response.LiveRemote.Relation, "not checked"))
		if response.LiveRemote.Commit != "" {
			r.field("live commit", response.LiveRemote.Commit)
		}
		if response.LiveRemote.Detail != "" {
			r.field("live detail", response.LiveRemote.Detail)
		}
	}
	for _, warning := range response.Warnings {
		r.warning(warning)
	}
	for _, action := range response.RecommendedActions {
		r.field("next", action)
	}
}

func (r *Renderer) History(response app.HistoryResponse) {
	r.heading("History", response.Scope)
	if response.ProjectRoot != "" {
		r.field("project", response.ProjectRoot)
	}
	r.field("recording enabled", fmt.Sprint(response.RecordingEnabled))
	if len(response.Records) == 0 {
		r.field("records", "none")
		return
	}
	fmt.Fprintf(r.w, "%s (%d):\n", r.label("records"), len(response.Records))
	for _, record := range response.Records {
		r.bullet(fmt.Sprintf("%s %s %s", record.FinishedAt.Format("2006-01-02T15:04:05Z07:00"), record.Workflow, record.Status))
		if response.Scope == "all" {
			r.indentedField("project", fallback(record.ProjectRoot, "unknown"))
		}
		if record.WorkflowID != "" {
			r.indentedField("workflow", record.WorkflowID)
		}
		r.indentedField("execution", record.ExecutionID)
		r.indentedField("plan", record.PlanID+" ("+record.PlanDigest+")")
		for _, step := range record.Steps {
			r.indentedField("step", step.CapabilityID+" "+string(step.Status))
		}
		for _, hint := range record.RecoveryHints {
			r.indentedField("recovery", hint)
		}
	}
	if response.HasMore {
		r.field("next", "more records are available; increase --limit up to 200")
	}
}

func (r *Renderer) CheckStarted(capabilityID string, index, total int) {
	fmt.Fprintf(r.w, "%s check %d/%d  %s\n", r.symbol("running"), index, total, r.strong(capabilityID))
}

func (r *Renderer) CheckFinished(result golangadapter.CheckResult) {
	fmt.Fprintf(r.w, "%s check      %s %s\n", r.symbol(result.Status), r.strong(result.CapabilityID), result.Status)
	if result.Status == "failed" && result.Diagnostic != "" {
		r.field("diagnostic", result.Diagnostic)
	}
}

func (r *Renderer) planHeader(plan operation.Plan) {
	fmt.Fprintf(r.w, "%s %s %s\n", r.symbol("plan"), r.strong("Plan"), plan.ID)
	r.field("digest", plan.Digest)
	r.field("project", plan.ProjectRoot)
	r.field("dry-run", fmt.Sprint(plan.DryRun))
}

func (r *Renderer) operations(operations []operation.Operation) {
	fmt.Fprintln(r.w, r.label("operations")+":")
	for _, op := range operations {
		r.bullet(fmt.Sprintf("%s [%s] %s", op.CapabilityID, op.Risk, op.Summary))
		for _, effect := range op.Effects {
			r.indentedField("effect", effect.Kind+" "+effect.Target)
		}
	}
}

func (r *Renderer) paths(label string, paths []string) {
	fmt.Fprintln(r.w, r.label(label)+":")
	if len(paths) <= 100 {
		for _, path := range paths {
			r.bullet(path)
		}
		return
	}
	groups := map[string][]string{}
	for _, path := range paths {
		parts := strings.Split(path, "/")
		prefix := path
		if len(parts) >= 2 {
			prefix = strings.Join(parts[:2], "/")
		}
		groups[prefix] = append(groups[prefix], path)
	}
	prefixes := make([]string, 0, len(groups))
	for prefix := range groups {
		prefixes = append(prefixes, prefix)
	}
	sort.Strings(prefixes)
	for _, prefix := range prefixes {
		group := groups[prefix]
		if len(group) > 10 && strings.Contains(group[0], "/") {
			r.bullet(fmt.Sprintf("%s/** (%d paths)", prefix, len(group)))
			continue
		}
		for _, path := range group {
			r.bullet(path)
		}
	}
	r.indentedField("note", "exact paths are retained in the plan digest and available with --plan-json")
}

func (r *Renderer) statusPaths(label string, paths []string) {
	if len(paths) == 0 {
		r.field(label, "none")
		return
	}
	r.paths(fmt.Sprintf("%s (%d)", label, len(paths)), paths)
}

func (r *Renderer) heading(label, value string) {
	fmt.Fprintf(r.w, "%s %s: %s\n", r.symbol(value), r.strong(label), value)
}

func (r *Renderer) field(label, value string) {
	r.writeWrapped("", r.label(label)+": ", value)
}

func (r *Renderer) indentedField(label, value string) {
	r.writeWrapped("    ", label+": ", value)
}

func (r *Renderer) warning(value string) {
	r.writeWrapped("", r.paint("warning", "33")+": ", value)
}

func (r *Renderer) bullet(value string) {
	r.writeWrapped("  ", r.symbol("bullet")+" ", value)
}

func (r *Renderer) writeWrapped(indent, prefix, value string) {
	prefixWidth := visibleRunes(indent + prefix)
	available := r.width - prefixWidth
	if available < 12 {
		available = 12
	}
	lines := wrap(value, available)
	if len(lines) == 0 {
		lines = []string{""}
	}
	fmt.Fprintln(r.w, indent+prefix+lines[0])
	continuation := indent + strings.Repeat(" ", visibleRunes(prefix))
	for _, line := range lines[1:] {
		fmt.Fprintln(r.w, continuation+line)
	}
}

func visibleRunes(value string) int {
	visible := 0
	inEscape := false
	for _, current := range value {
		if current == '\x1b' {
			inEscape = true
			continue
		}
		if inEscape {
			if current == 'm' {
				inEscape = false
			}
			continue
		}
		visible++
	}
	return visible
}

func wrap(value string, width int) []string {
	if utf8.RuneCountInString(value) <= width {
		return []string{value}
	}
	var lines []string
	for _, paragraph := range strings.Split(value, "\n") {
		words := strings.Fields(paragraph)
		line := ""
		for _, word := range words {
			if line == "" {
				for utf8.RuneCountInString(word) > width {
					runes := []rune(word)
					lines = append(lines, string(runes[:width]))
					word = string(runes[width:])
				}
				line = word
				continue
			}
			if utf8.RuneCountInString(line)+1+utf8.RuneCountInString(word) > width {
				lines = append(lines, line)
				line = word
			} else {
				line += " " + word
			}
		}
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func (r *Renderer) label(value string) string  { return r.paint(value, "36") }
func (r *Renderer) strong(value string) string { return r.paint(value, "1") }

func (r *Renderer) paint(value, code string) string {
	if !r.color {
		return value
	}
	return "\x1b[" + code + "m" + value + "\x1b[0m"
}

func (r *Renderer) symbol(status string) string {
	if !r.unicode {
		switch status {
		case "succeeded", "clean", "valid", "installed":
			return r.paint("OK", "32")
		case "failed", "error", "invalid":
			return r.paint("X", "31")
		case "warning", "dirty", "partially_completed":
			return r.paint("!", "33")
		case "running":
			return r.paint(">", "36")
		case "bullet":
			return "-"
		default:
			return r.paint("-", "36")
		}
	}
	switch status {
	case "succeeded", "clean", "valid", "installed":
		return r.paint("✓", "32")
	case "failed", "error", "invalid":
		return r.paint("×", "31")
	case "warning", "dirty", "partially_completed":
		return r.paint("!", "33")
	case "running":
		return r.paint("→", "36")
	case "bullet":
		return "•"
	default:
		return r.paint("◆", "36")
	}
}

func findOperation(plan operation.Plan, capabilityID string) (operation.Operation, bool) {
	for _, op := range plan.Operations {
		if op.CapabilityID == capabilityID {
			return op, true
		}
	}
	return operation.Operation{}, false
}

func countChecks(plan operation.Plan) int {
	count := 0
	for _, op := range plan.Operations {
		if strings.HasPrefix(op.CapabilityID, "go.") {
			count++
		}
	}
	return count
}

func fallback(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
