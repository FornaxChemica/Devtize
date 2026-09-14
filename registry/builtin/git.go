// Package builtin contains reviewed command knowledge shipped with Devtize.
// Entries are discovery metadata only and cannot be executed.
package builtin

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/FornaxChemica/devtize/internal/registry"
	"github.com/FornaxChemica/devtize/internal/safety"
)

const locator = "registry/builtin/git.go"

func Git() []registry.CommandKnowledge {
	commands := []registry.CommandKnowledge{
		entry("git.init", []string{"git", "init"}, "Create an empty Git repository in a folder.", []string{"initialize repository"}, []string{"initialize git repository", "start a git repository"}, []string{"git init"}, ">=2.23", safety.RiskLocalWrite, "creates Git metadata in the selected folder"),
		entry("git.status", []string{"git", "status"}, "Show the working tree and index status.", []string{"working tree status"}, []string{"show repository status", "what files changed"}, []string{"git status --short"}, ">=2.23", safety.RiskReadOnly, "reads repository and index state"),
		entry("git.diff", []string{"git", "diff"}, "Show changes between working tree, index, or commits.", []string{"show changes"}, []string{"show working tree changes", "compare git changes"}, []string{"git diff", "git diff --staged"}, ">=2.23", safety.RiskReadOnly, "reads file and repository differences"),
		entry("git.add", []string{"git", "add"}, "Stage selected content for the next commit.", []string{"stage files"}, []string{"add files to git", "stage changes"}, []string{"git add -- path/to/file"}, ">=2.23", safety.RiskLocalWrite, "changes the Git index"),
		entry("git.commit", []string{"git", "commit"}, "Create a commit from staged content.", []string{"record changes"}, []string{"commit staged changes", "create git commit"}, []string{"git commit -m <message>"}, ">=2.23", safety.RiskLocalWrite, "creates a local commit"),
		entry("git.branch", []string{"git", "branch"}, "List local branches.", []string{"list branches"}, []string{"show git branches"}, []string{"git branch --list"}, ">=2.23", safety.RiskReadOnly, "reads local branch references when used as shown"),
		entry("git.log", []string{"git", "log"}, "Show commit history.", []string{"commit history"}, []string{"show git history", "recent commits"}, []string{"git log --oneline"}, ">=2.23", safety.RiskReadOnly, "reads commit history"),
		entry("git.remote", []string{"git", "remote"}, "List configured repository remotes.", []string{"list remotes"}, []string{"show git remotes", "remote repository url"}, []string{"git remote -v"}, ">=2.23", safety.RiskReadOnly, "reads configured remote names and URLs when used as shown"),
		entry("git.push", []string{"git", "push"}, "Send local commits to a remote repository.", []string{"upload commits"}, []string{"push branch", "publish git commits"}, []string{"git push --set-upstream origin <branch>"}, ">=2.23", safety.RiskRemoteWrite, "updates references and objects on a remote repository"),
		entry("git.pull", []string{"git", "pull"}, "Fetch and integrate changes from a remote branch.", []string{"update branch"}, []string{"pull remote changes", "download and merge git changes"}, []string{"git pull --ff-only"}, ">=2.23", safety.RiskLocalWrite, "downloads remote data and may update the local branch and working tree"),
		entry("git.restore", []string{"git", "restore"}, "Restore working-tree or staged content from another source.", []string{"discard file changes"}, []string{"restore git file", "unstage file"}, []string{"git restore --staged -- path/to/file"}, ">=2.23", safety.RiskDestructive, "may overwrite uncommitted working-tree content"),
		entry("git.reset-soft", []string{"git", "reset", "--soft"}, "Move HEAD while preserving index and working-tree changes.", []string{"undo commit keep changes"}, []string{"soft reset git commit"}, []string{"git reset --soft HEAD~1"}, ">=2.23", safety.RiskLocalWrite, "moves the local HEAD reference while preserving file changes"),
	}
	payload, _ := json.Marshal(commands)
	sum := sha256.Sum256(payload)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	for index := range commands {
		commands[index].Source.Digest = digest
	}
	return commands
}

func Catalog() (*registry.Catalog, error) {
	return registry.NewCatalog(Git())
}

func entry(id string, path []string, summary string, aliases, phrases, examples []string, version string, risk safety.Risk, effect string) registry.CommandKnowledge {
	return registry.CommandKnowledge{
		ID: id, ProviderID: "git", CommandPath: path, Summary: summary, Aliases: aliases,
		IntentPhrases: phrases, Examples: examples, VersionRange: version, Risk: risk,
		Effects: []string{effect}, Source: registry.KnowledgeSource{Kind: "builtin", Locator: locator},
		Support: registry.SupportDiscoverable,
	}
}
