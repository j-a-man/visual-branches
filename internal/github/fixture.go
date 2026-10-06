package github

import (
	"encoding/json"
	"os"
	"time"

	"github.com/j-a-man/visual-branches/internal/model"
)

// fixturePRs loads pull requests from a JSON file mapping head branch names
// to PRs, set with VB_GITHUB_FIXTURE. It exists for tests, demos, and
// screenshots, and never touches the network.
func fixturePRs(path string, heads []string) (map[string]*model.PR, model.GitHubStatus) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, model.GitHubStatus{Status: model.GitHubError, Message: "fixture: " + err.Error()}
	}
	var all map[string]*model.PR
	if err := json.Unmarshal(data, &all); err != nil {
		return nil, model.GitHubStatus{Status: model.GitHubError, Message: "fixture: " + err.Error()}
	}
	out := map[string]*model.PR{}
	for _, h := range heads {
		if pr, ok := all[h]; ok {
			out[h] = pr
		}
	}
	now := time.Now().Add(-12 * time.Second)
	return out, model.GitHubStatus{Status: model.GitHubOK, FetchedAt: &now}
}
