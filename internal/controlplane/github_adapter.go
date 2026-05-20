package controlplane

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const GitHubProvider = "github"
const githubLogExcerptLimit = 8000

type githubFileChange struct {
	Path    string
	Content string
}

type GitHubAdapterConfig struct {
	Token   string
	BaseURL string
	Client  *http.Client
}

type GitHubAdapter struct {
	token   string
	baseURL string
	client  *http.Client
}

func NewGitHubAdapter(config GitHubAdapterConfig) GitHubAdapter {
	baseURL := strings.TrimRight(config.BaseURL, "/")
	if baseURL == "" {
		baseURL = "https://api.github.com"
	}
	client := config.Client
	if client == nil {
		client = http.DefaultClient
	}
	return GitHubAdapter{
		token:   config.Token,
		baseURL: baseURL,
		client:  client,
	}
}

func (a GitHubAdapter) Execute(definition CapabilityDefinition, req ToolCallRequest) (map[string]any, error) {
	if strings.TrimSpace(a.token) == "" {
		return nil, errors.New("github adapter requires GITHUB_TOKEN")
	}

	switch definition.ID {
	case "ci.get_checks":
		return a.getChecks(req)
	case "ci.get_logs":
		return a.getLogs(req)
	case "code_host.get_recent_changes":
		return a.getRecentChanges(req)
	case "code_host.get_file":
		return a.getFile(req)
	case "code_host.get_pull_request":
		return a.getPullRequest(req)
	case "code_host.create_draft_pr":
		return a.createDraftPR(req)
	case "deploy.get_recent_deploys":
		return a.getRecentDeploys(req)
	default:
		return nil, fmt.Errorf("github adapter does not support capability '%s'", definition.ID)
	}
}

func (a GitHubAdapter) createDraftPR(req ToolCallRequest) (map[string]any, error) {
	owner, repo, err := githubRepoArgs("code_host.create_draft_pr", req.Arguments)
	if err != nil {
		return nil, err
	}
	title, ok := stringArg(req.Arguments, "title")
	if !ok {
		return nil, fmt.Errorf("github code_host.create_draft_pr requires title argument")
	}
	head, ok := githubHeadBranchArg(req.Arguments)
	if !ok {
		return nil, fmt.Errorf("github code_host.create_draft_pr requires head, head_branch, or branch argument")
	}
	base, ok := stringArg(req.Arguments, "base")
	if !ok {
		base, ok = stringArg(req.Arguments, "base_branch")
	}
	if !ok {
		base = "main"
	}
	body, _ := stringArg(req.Arguments, "body")
	draft := optionalBoolArg(req.Arguments, "draft", true)
	files, hasFiles, err := githubFileChangesArg(req.Arguments)
	if err != nil {
		return nil, err
	}
	if hasFiles {
		commitMessage, ok := stringArg(req.Arguments, "commit_message")
		if !ok {
			commitMessage = fmt.Sprintf("Apply %s changes", head)
		}
		if err := a.ensureGitHubBranch(owner, repo, base, head); err != nil {
			return nil, err
		}
		if err := a.upsertGitHubFiles(owner, repo, head, commitMessage, files); err != nil {
			return nil, err
		}
	}

	payload := map[string]any{
		"title": title,
		"head":  head,
		"base":  base,
		"body":  body,
		"draft": draft,
	}
	var response githubCreatePullResponse
	path := fmt.Sprintf("/repos/%s/%s/pulls", url.PathEscape(owner), url.PathEscape(repo))
	if err := a.postJSON(path, payload, &response); err != nil {
		return nil, err
	}
	if response.Number == 0 || response.HTMLURL == "" {
		return nil, fmt.Errorf("github create pull request response did not include PR number and URL")
	}
	branch := response.Head.Ref
	if branch == "" {
		branch = head
	}
	return map[string]any{
		"pr_number":  response.Number,
		"repository": fmt.Sprintf("%s/%s", owner, repo),
		"owner":      owner,
		"repo":       repo,
		"branch":     branch,
		"head":       branch,
		"base":       base,
		"head_sha":   response.Head.SHA,
		"title":      response.Title,
		"url":        response.HTMLURL,
		"source_url": response.HTMLURL,
		"draft":      response.Draft,
		"evidence":   fmt.Sprintf("GitHub draft PR #%d created for %s/%s from %s into %s.", response.Number, owner, repo, head, base),
	}, nil
}

func (a GitHubAdapter) getPullRequest(req ToolCallRequest) (map[string]any, error) {
	owner, repo, err := githubRepoArgs("code_host.get_pull_request", req.Arguments)
	if err != nil {
		return nil, err
	}
	number, err := intArg(req.Arguments, "pr_number")
	if err != nil {
		number, err = intArg(req.Arguments, "number")
	}
	if err != nil || number <= 0 {
		return nil, fmt.Errorf("github code_host.get_pull_request requires pr_number or number argument")
	}
	var response githubPullDetailResponse
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d", url.PathEscape(owner), url.PathEscape(repo), number)
	if err := a.getJSON(path, &response); err != nil {
		return nil, err
	}
	if response.Number == 0 {
		response.Number = number
	}
	return map[string]any{
		"pr_number":        response.Number,
		"repository":       fmt.Sprintf("%s/%s", owner, repo),
		"owner":            owner,
		"repo":             repo,
		"state":            response.State,
		"merged":           response.Merged,
		"merged_at":        response.MergedAt,
		"merge_commit_sha": response.MergeCommitSHA,
		"branch":           response.Head.Ref,
		"head_sha":         response.Head.SHA,
		"base":             response.Base.Ref,
		"title":            response.Title,
		"url":              response.HTMLURL,
		"source_url":       response.HTMLURL,
		"draft":            response.Draft,
		"evidence":         fmt.Sprintf("GitHub PR #%d for %s/%s is %s; merged=%t.", response.Number, owner, repo, response.State, response.Merged),
	}, nil
}

func (a GitHubAdapter) getFile(req ToolCallRequest) (map[string]any, error) {
	owner, repo, err := githubRepoArgs("code_host.get_file", req.Arguments)
	if err != nil {
		return nil, err
	}
	filePath, ok := stringArg(req.Arguments, "path")
	if !ok {
		return nil, fmt.Errorf("github code_host.get_file requires path argument")
	}
	file, err := newGitHubFileChange(filePath, "")
	if err != nil {
		return nil, err
	}
	ref, _ := stringArg(req.Arguments, "ref")
	if ref == "" {
		ref, _ = stringArg(req.Arguments, "branch")
	}
	if ref == "" {
		ref, _ = stringArg(req.Arguments, "base")
	}
	query := url.Values{}
	if ref != "" {
		query.Set("ref", ref)
	}
	path := githubContentAPIPath(owner, repo, file.Path)
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var response githubContentResponse
	if err := a.getJSON(path, &response); err != nil {
		return nil, err
	}
	if strings.TrimSpace(response.Content) == "" {
		return nil, fmt.Errorf("github content response did not include file content")
	}
	if response.Encoding != "" && response.Encoding != "base64" {
		return nil, fmt.Errorf("github content response used unsupported encoding %q", response.Encoding)
	}
	content, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(response.Content, "\n", ""))
	if err != nil {
		return nil, fmt.Errorf("decode github file content: %w", err)
	}
	sourceURL := response.HTMLURL
	if sourceURL == "" {
		sourceURL = fmt.Sprintf("https://github.com/%s/%s/blob/%s/%s", owner, repo, firstNonEmpty(ref, "HEAD"), file.Path)
	}
	return map[string]any{
		"repository": fmt.Sprintf("%s/%s", owner, repo),
		"owner":      owner,
		"repo":       repo,
		"path":       file.Path,
		"ref":        ref,
		"sha":        response.SHA,
		"content":    string(content),
		"source_url": sourceURL,
		"evidence":   fmt.Sprintf("Read %s from %s/%s.", file.Path, owner, repo),
	}, nil
}

func (a GitHubAdapter) getRecentChanges(req ToolCallRequest) (map[string]any, error) {
	owner, repo, err := githubRepoArgs("code_host.get_recent_changes", req.Arguments)
	if err != nil {
		return nil, err
	}
	limit := optionalIntArg(req.Arguments, "limit", 5)
	if limit < 1 {
		limit = 1
	}
	if limit > 20 {
		limit = 20
	}

	query := url.Values{}
	query.Set("state", "closed")
	query.Set("sort", "updated")
	query.Set("direction", "desc")
	query.Set("per_page", strconv.Itoa(limit*2))
	if branch, ok := stringArg(req.Arguments, "branch"); ok {
		query.Set("base", branch)
	}

	var pulls []githubPullListItem
	path := fmt.Sprintf("/repos/%s/%s/pulls?%s", url.PathEscape(owner), url.PathEscape(repo), query.Encode())
	if err := a.getJSON(path, &pulls); err != nil {
		return nil, err
	}

	changes := make([]map[string]any, 0, limit)
	for _, pull := range pulls {
		if pull.MergedAt == "" {
			continue
		}
		changes = append(changes, map[string]any{
			"pr":            pull.Number,
			"title":         pull.Title,
			"author":        pull.User.Login,
			"merged_at":     pull.MergedAt,
			"updated_at":    pull.UpdatedAt,
			"changed_files": pull.ChangedFiles,
			"url":           pull.HTMLURL,
			"summary":       fmt.Sprintf("Merged PR #%d: %s", pull.Number, pull.Title),
		})
		if len(changes) == limit {
			break
		}
	}

	sourceURL := fmt.Sprintf("%s/%s/%s/pulls?q=is%%3Apr+is%%3Amerged", strings.TrimRight("https://github.com", "/"), owner, repo)
	return map[string]any{
		"changes":    changes,
		"evidence":   fmt.Sprintf("GitHub returned %d merged pull request change(s) for %s/%s.", len(changes), owner, repo),
		"source_url": sourceURL,
	}, nil
}

func (a GitHubAdapter) getRecentDeploys(req ToolCallRequest) (map[string]any, error) {
	owner, repo, err := githubRepoArgs("deploy.get_recent_deploys", req.Arguments)
	if err != nil {
		return nil, err
	}
	branch := firstStringArg(req.Arguments, "branch", "ref", "base")
	commitSHA := firstStringArg(req.Arguments, "commit_sha", "sha", "head_sha")
	workflow := firstStringArg(req.Arguments, "workflow", "workflow_id")
	limit := optionalIntArg(req.Arguments, "limit", 5)
	if limit <= 0 {
		limit = 5
	}
	if limit > 20 {
		limit = 20
	}

	query := url.Values{}
	query.Set("per_page", strconv.Itoa(limit))
	if branch != "" {
		query.Set("branch", branch)
	}
	if commitSHA != "" {
		query.Set("head_sha", commitSHA)
	}

	basePath := fmt.Sprintf("/repos/%s/%s/actions/runs", url.PathEscape(owner), url.PathEscape(repo))
	if workflow != "" {
		basePath = fmt.Sprintf("/repos/%s/%s/actions/workflows/%s/runs", url.PathEscape(owner), url.PathEscape(repo), url.PathEscape(workflow))
	}
	path := basePath + "?" + query.Encode()

	var response githubWorkflowRunsResponse
	if err := a.getJSON(path, &response); err != nil {
		return nil, err
	}

	deploys := make([]map[string]any, 0, len(response.WorkflowRuns))
	sourceURL := ""
	for _, run := range response.WorkflowRuns {
		status := normalizeGitHubWorkflowRunStatus(run.Status, run.Conclusion)
		item := map[string]any{
			"id":          run.ID,
			"workflow":    firstNonEmpty(run.Name, workflow),
			"status":      status,
			"conclusion":  run.Conclusion,
			"branch":      run.HeadBranch,
			"commit_sha":  run.HeadSHA,
			"event":       run.Event,
			"started_at":  firstNonEmpty(run.RunStartedAt, run.CreatedAt),
			"updated_at":  run.UpdatedAt,
			"url":         run.HTMLURL,
			"source_url":  run.HTMLURL,
			"workflow_id": run.WorkflowID,
		}
		deploys = append(deploys, item)
		if sourceURL == "" {
			sourceURL = run.HTMLURL
		}
	}

	status := inferGitHubDeploymentStatus(deploys)
	target := firstNonEmpty(commitSHA, branch, "latest")
	evidence := fmt.Sprintf("GitHub returned %d workflow run(s) for %s/%s target %s.", len(deploys), owner, repo, target)
	return map[string]any{
		"status":     status,
		"deploys":    deploys,
		"workflow":   workflow,
		"branch":     branch,
		"commit_sha": commitSHA,
		"evidence":   evidence,
		"source_url": sourceURL,
	}, nil
}

func (a GitHubAdapter) getChecks(req ToolCallRequest) (map[string]any, error) {
	owner, repo, err := githubRepoArgs("ci.get_checks", req.Arguments)
	if err != nil {
		return nil, err
	}
	ref, err := githubRefArg(req.Arguments)
	if err != nil {
		prNumber, prErr := intArg(req.Arguments, "pr_number")
		if prErr != nil {
			return nil, err
		}
		ref, err = a.pullRequestHeadSHA(owner, repo, prNumber)
		if err != nil {
			return nil, err
		}
	}

	var response githubCheckRunsResponse
	path := fmt.Sprintf("/repos/%s/%s/commits/%s/check-runs", url.PathEscape(owner), url.PathEscape(repo), url.PathEscape(ref))
	if err := a.getJSON(path, &response); err != nil {
		return nil, err
	}

	checks := make([]map[string]any, 0, len(response.CheckRuns))
	status := "passed"
	sourceURL := ""
	for _, run := range response.CheckRuns {
		check := map[string]any{
			"name":       run.Name,
			"status":     run.Status,
			"conclusion": run.Conclusion,
			"url":        run.HTMLURL,
		}
		checks = append(checks, check)
		if sourceURL == "" {
			sourceURL = run.HTMLURL
		}
		status = combineGitHubCheckStatus(status, run.Status, run.Conclusion)
	}
	if len(response.CheckRuns) == 0 {
		status = "pending"
	}
	failedJob := map[string]any(nil)
	if status == "failed" {
		failedJob, _ = a.discoverFailedGitHubActionsJob(owner, repo, ref)
		if failedJob != nil {
			if jobName, ok := failedJob["name"].(string); ok && jobName != "" {
				for index, check := range checks {
					if check["name"] == jobName {
						checks[index]["job_id"] = failedJob["job_id"]
						checks[index]["logs_url"] = failedJob["logs_url"]
						break
					}
				}
			}
		}
	}

	evidence := fmt.Sprintf("GitHub returned %d check run(s) for %s/%s@%s.", len(response.CheckRuns), owner, repo, ref)
	result := map[string]any{
		"status":     status,
		"commit_sha": ref,
		"checks":     checks,
		"evidence":   evidence,
		"source_url": sourceURL,
	}
	if failedJob != nil {
		result["job_id"] = failedJob["job_id"]
		result["logs_url"] = failedJob["logs_url"]
		result["failed_job"] = failedJob
	}
	return result, nil
}

func (a GitHubAdapter) discoverFailedGitHubActionsJob(owner string, repo string, headSHA string) (map[string]any, error) {
	query := url.Values{}
	query.Set("head_sha", headSHA)
	query.Set("per_page", "5")
	runsPath := fmt.Sprintf("/repos/%s/%s/actions/runs?%s", url.PathEscape(owner), url.PathEscape(repo), query.Encode())
	var runs githubWorkflowRunsResponse
	if err := a.getJSON(runsPath, &runs); err != nil {
		return nil, err
	}
	for _, run := range runs.WorkflowRuns {
		if run.ID == 0 {
			continue
		}
		jobsPath := fmt.Sprintf("/repos/%s/%s/actions/runs/%d/jobs?filter=latest&per_page=100", url.PathEscape(owner), url.PathEscape(repo), run.ID)
		var jobs githubWorkflowJobsResponse
		if err := a.getJSON(jobsPath, &jobs); err != nil {
			return nil, err
		}
		for _, job := range jobs.Jobs {
			if !isFailingGitHubConclusion(job.Conclusion, job.Status) {
				continue
			}
			logsURL := job.LogsURL
			if logsURL == "" {
				logsURL = fmt.Sprintf("/repos/%s/%s/actions/jobs/%d/logs", url.PathEscape(owner), url.PathEscape(repo), job.ID)
			}
			return map[string]any{
				"job_id":     job.ID,
				"name":       job.Name,
				"status":     job.Status,
				"conclusion": job.Conclusion,
				"url":        job.HTMLURL,
				"source_url": job.HTMLURL,
				"logs_url":   logsURL,
				"run_id":     run.ID,
				"run_url":    run.HTMLURL,
			}, nil
		}
	}
	return nil, nil
}

func (a GitHubAdapter) getLogs(req ToolCallRequest) (map[string]any, error) {
	logsURL, ok := stringArg(req.Arguments, "logs_url")
	if !ok {
		owner, repo, err := githubRepoArgs("ci.get_logs", req.Arguments)
		if err != nil {
			return nil, err
		}
		jobID, err := intArg(req.Arguments, "job_id")
		if err != nil {
			return nil, fmt.Errorf("github ci.get_logs requires logs_url or repository plus job_id arguments")
		}
		logsURL = fmt.Sprintf("/repos/%s/%s/actions/jobs/%d/logs", url.PathEscape(owner), url.PathEscape(repo), jobID)
	}

	logText, finalURL, err := a.getText(logsURL)
	if err != nil {
		return nil, err
	}
	excerpt, truncated := boundedText(logText, githubLogExcerptLimit)
	return map[string]any{
		"summary":     summarizeGitHubLog(logText),
		"log_excerpt": excerpt,
		"truncated":   truncated,
		"source_url":  finalURL,
		"evidence":    fmt.Sprintf("Fetched %d byte(s) of GitHub CI logs.", len(logText)),
	}, nil
}

func (a GitHubAdapter) ensureGitHubBranch(owner string, repo string, base string, head string) error {
	headPath := fmt.Sprintf("/repos/%s/%s/git/ref/heads/%s", url.PathEscape(owner), url.PathEscape(repo), url.PathEscape(head))
	if exists, err := a.githubRefExists(headPath); err != nil {
		return err
	} else if exists {
		return nil
	}

	var baseRef githubRefResponse
	basePath := fmt.Sprintf("/repos/%s/%s/git/ref/heads/%s", url.PathEscape(owner), url.PathEscape(repo), url.PathEscape(base))
	if err := a.getJSON(basePath, &baseRef); err != nil {
		return err
	}
	if baseRef.Object.SHA == "" {
		return fmt.Errorf("github base branch %s did not include object SHA", base)
	}

	payload := map[string]any{
		"ref": fmt.Sprintf("refs/heads/%s", head),
		"sha": baseRef.Object.SHA,
	}
	var created githubRefResponse
	if err := a.postJSON(fmt.Sprintf("/repos/%s/%s/git/refs", url.PathEscape(owner), url.PathEscape(repo)), payload, &created); err != nil {
		return err
	}
	return nil
}

func (a GitHubAdapter) githubRefExists(path string) (bool, error) {
	body, _, status, err := a.doStatus(http.MethodGet, path, "application/vnd.github+json", nil)
	if err != nil {
		return false, err
	}
	if status == http.StatusNotFound {
		return false, nil
	}
	if status < 200 || status > 299 {
		return false, fmt.Errorf("github API request failed with status %d: %s", status, strings.TrimSpace(string(body)))
	}
	return true, nil
}

func (a GitHubAdapter) upsertGitHubFiles(owner string, repo string, branch string, commitMessage string, files []githubFileChange) error {
	for _, file := range files {
		existingSHA, err := a.githubFileSHA(owner, repo, branch, file.Path)
		if err != nil {
			return err
		}
		payload := map[string]any{
			"message": commitMessage,
			"content": base64.StdEncoding.EncodeToString([]byte(file.Content)),
			"branch":  branch,
		}
		if existingSHA != "" {
			payload["sha"] = existingSHA
		}
		var response map[string]any
		contentPath := githubContentAPIPath(owner, repo, file.Path)
		if err := a.putJSON(contentPath, payload, &response); err != nil {
			return err
		}
	}
	return nil
}

func (a GitHubAdapter) githubFileSHA(owner string, repo string, branch string, filePath string) (string, error) {
	requestPath := githubContentAPIPath(owner, repo, filePath) + "?ref=" + url.QueryEscape(branch)
	body, _, status, err := a.doStatus(http.MethodGet, requestPath, "application/vnd.github+json", nil)
	if err != nil {
		return "", err
	}
	if status == http.StatusNotFound {
		return "", nil
	}
	if status < 200 || status > 299 {
		return "", fmt.Errorf("github API request failed with status %d: %s", status, strings.TrimSpace(string(body)))
	}
	var response githubContentResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return "", err
	}
	return response.SHA, nil
}

func (a GitHubAdapter) pullRequestHeadSHA(owner string, repo string, prNumber int) (string, error) {
	var response githubPullResponse
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d", url.PathEscape(owner), url.PathEscape(repo), prNumber)
	if err := a.getJSON(path, &response); err != nil {
		return "", err
	}
	if response.Head.SHA == "" {
		return "", fmt.Errorf("github pull request #%d did not include head SHA", prNumber)
	}
	return response.Head.SHA, nil
}

func (a GitHubAdapter) getJSON(path string, target any) error {
	body, _, err := a.get(path, "application/vnd.github+json")
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, target); err != nil {
		return err
	}
	return nil
}

func (a GitHubAdapter) postJSON(path string, payload any, target any) error {
	return a.writeJSON(http.MethodPost, path, payload, target)
}

func (a GitHubAdapter) putJSON(path string, payload any, target any) error {
	return a.writeJSON(http.MethodPut, path, payload, target)
}

func (a GitHubAdapter) writeJSON(method string, path string, payload any, target any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	body, _, err := a.do(method, path, "application/vnd.github+json", strings.NewReader(string(encoded)))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, target); err != nil {
		return err
	}
	return nil
}

func (a GitHubAdapter) getText(pathOrURL string) (string, string, error) {
	body, finalURL, err := a.get(pathOrURL, "application/vnd.github+json")
	if err != nil {
		return "", "", err
	}
	return string(body), finalURL, nil
}

func (a GitHubAdapter) get(pathOrURL string, accept string) ([]byte, string, error) {
	return a.do(http.MethodGet, pathOrURL, accept, nil)
}

func (a GitHubAdapter) do(method string, pathOrURL string, accept string, requestBody io.Reader) ([]byte, string, error) {
	body, requestURL, status, err := a.doStatus(method, pathOrURL, accept, requestBody)
	if err != nil {
		return nil, "", err
	}
	if status < 200 || status > 299 {
		return nil, "", fmt.Errorf("github API request failed with status %d: %s", status, strings.TrimSpace(string(body)))
	}
	return body, requestURL, nil
}

func (a GitHubAdapter) doStatus(method string, pathOrURL string, accept string, requestBody io.Reader) ([]byte, string, int, error) {
	requestURL := pathOrURL
	if !strings.HasPrefix(pathOrURL, "http://") && !strings.HasPrefix(pathOrURL, "https://") {
		requestURL = a.baseURL + pathOrURL
	}
	httpReq, err := http.NewRequest(method, requestURL, requestBody)
	if err != nil {
		return nil, "", 0, err
	}
	httpReq.Header.Set("Accept", accept)
	httpReq.Header.Set("Authorization", "Bearer "+a.token)
	httpReq.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if requestBody != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}

	httpResp, err := a.client.Do(httpReq)
	if err != nil {
		return nil, "", 0, err
	}
	defer httpResp.Body.Close()

	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, "", 0, err
	}
	return body, requestURL, httpResp.StatusCode, nil
}

func githubRepoArgs(action string, args map[string]any) (string, string, error) {
	if repository, ok := stringArg(args, "repository"); ok {
		parts := strings.Split(repository, "/")
		if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
			return parts[0], parts[1], nil
		}
		return "", "", fmt.Errorf("repository must use owner/repo format")
	}

	owner, ownerOK := stringArg(args, "owner")
	repo, repoOK := stringArg(args, "repo")
	if !repoOK {
		repo, repoOK = stringArg(args, "repository_name")
	}
	if !ownerOK || !repoOK {
		return "", "", fmt.Errorf("github %s requires repository or owner and repo arguments", action)
	}
	return owner, repo, nil
}

func githubRefArg(args map[string]any) (string, error) {
	for _, key := range []string{"ref", "commit_sha", "sha", "head_sha"} {
		if value, ok := stringArg(args, key); ok {
			return value, nil
		}
	}
	return "", fmt.Errorf("github ci.get_checks requires ref, commit_sha, sha, head_sha, or pr_number argument")
}

func githubHeadBranchArg(args map[string]any) (string, bool) {
	for _, key := range []string{"head", "head_branch", "branch"} {
		if value, ok := stringArg(args, key); ok {
			return value, true
		}
	}
	return "", false
}

func githubFileChangesArg(args map[string]any) ([]githubFileChange, bool, error) {
	if path, ok := stringArg(args, "file_path"); ok {
		content, contentOK := stringArg(args, "file_content")
		if !contentOK {
			return nil, true, fmt.Errorf("github code_host.create_draft_pr file_path requires file_content")
		}
		file, err := newGitHubFileChange(path, content)
		return []githubFileChange{file}, true, err
	}

	raw, ok := args["files"]
	if !ok {
		return nil, false, nil
	}
	switch typed := raw.(type) {
	case map[string]any:
		files := make([]githubFileChange, 0, len(typed))
		for filePath, content := range typed {
			text, ok := content.(string)
			if !ok {
				return nil, true, fmt.Errorf("github code_host.create_draft_pr files values must be strings")
			}
			file, err := newGitHubFileChange(filePath, text)
			if err != nil {
				return nil, true, err
			}
			files = append(files, file)
		}
		return files, len(files) > 0, nil
	case []any:
		files := make([]githubFileChange, 0, len(typed))
		for _, item := range typed {
			object, ok := item.(map[string]any)
			if !ok {
				return nil, true, fmt.Errorf("github code_host.create_draft_pr files entries must be objects")
			}
			path, pathOK := stringArg(object, "path")
			content, contentOK := stringArg(object, "content")
			if !pathOK || !contentOK {
				return nil, true, fmt.Errorf("github code_host.create_draft_pr files entries require path and content")
			}
			file, err := newGitHubFileChange(path, content)
			if err != nil {
				return nil, true, err
			}
			files = append(files, file)
		}
		return files, len(files) > 0, nil
	default:
		return nil, true, fmt.Errorf("github code_host.create_draft_pr files must be an object or array")
	}
}

func newGitHubFileChange(filePath string, content string) (githubFileChange, error) {
	filePath = strings.TrimSpace(filePath)
	if filePath == "" || strings.HasPrefix(filePath, "/") || strings.Contains(filePath, "..") {
		return githubFileChange{}, fmt.Errorf("github file path must be a relative repository path without '..'")
	}
	return githubFileChange{Path: filePath, Content: content}, nil
}

func githubContentAPIPath(owner string, repo string, filePath string) string {
	parts := strings.Split(filePath, "/")
	for index, part := range parts {
		parts[index] = url.PathEscape(part)
	}
	return fmt.Sprintf("/repos/%s/%s/contents/%s", url.PathEscape(owner), url.PathEscape(repo), strings.Join(parts, "/"))
}

func stringArg(args map[string]any, key string) (string, bool) {
	value, ok := args[key]
	if !ok {
		return "", false
	}
	text, ok := value.(string)
	if !ok {
		return "", false
	}
	text = strings.TrimSpace(text)
	return text, text != ""
}

func intArg(args map[string]any, key string) (int, error) {
	value, ok := args[key]
	if !ok {
		return 0, fmt.Errorf("missing %s argument", key)
	}
	switch typed := value.(type) {
	case int:
		return typed, nil
	case float64:
		return int(typed), nil
	case json.Number:
		result, err := typed.Int64()
		return int(result), err
	case string:
		result, err := strconv.Atoi(strings.TrimSpace(typed))
		if err != nil {
			return 0, fmt.Errorf("%s must be an integer", key)
		}
		return result, nil
	default:
		return 0, fmt.Errorf("%s must be an integer", key)
	}
}

func optionalIntArg(args map[string]any, key string, fallback int) int {
	value, err := intArg(args, key)
	if err != nil {
		return fallback
	}
	return value
}

func optionalBoolArg(args map[string]any, key string, fallback bool) bool {
	value, ok := args[key]
	if !ok {
		return fallback
	}
	typed, ok := value.(bool)
	if !ok {
		return fallback
	}
	return typed
}

func firstStringArg(args map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := stringArg(args, key); ok {
			return value
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func combineGitHubCheckStatus(current string, checkStatus string, conclusion string) string {
	failingConclusions := map[string]bool{
		"action_required": true,
		"cancelled":       true,
		"failure":         true,
		"startup_failure": true,
		"timed_out":       true,
	}
	if failingConclusions[conclusion] {
		return "failed"
	}
	if current == "failed" {
		return current
	}
	if checkStatus != "completed" || conclusion == "" {
		return "pending"
	}
	if current == "pending" {
		return current
	}
	return "passed"
}

func isFailingGitHubConclusion(conclusion string, status string) bool {
	switch conclusion {
	case "action_required", "cancelled", "failure", "startup_failure", "timed_out":
		return true
	}
	return status == "completed" && conclusion != "" && conclusion != "success" && conclusion != "neutral" && conclusion != "skipped"
}

func normalizeGitHubWorkflowRunStatus(status string, conclusion string) string {
	switch conclusion {
	case "failure", "cancelled", "timed_out", "action_required", "startup_failure":
		return "failed"
	case "success", "neutral", "skipped":
		return "succeeded"
	}
	if status == "completed" {
		return "failed"
	}
	if status == "queued" || status == "in_progress" || status == "requested" || status == "waiting" || status == "pending" {
		return "running"
	}
	if status != "" {
		return status
	}
	return "unknown"
}

func inferGitHubDeploymentStatus(deploys []map[string]any) string {
	if len(deploys) == 0 {
		return "not_started"
	}
	for _, deploy := range deploys {
		if deploy["status"] == "failed" {
			return "failed"
		}
	}
	for _, deploy := range deploys {
		if deploy["status"] == "running" {
			return "running"
		}
	}
	for _, deploy := range deploys {
		if deploy["status"] == "succeeded" {
			return "succeeded"
		}
	}
	return "unknown"
}

func boundedText(value string, limit int) (string, bool) {
	if len(value) <= limit {
		return value, false
	}
	return value[:limit], true
}

func summarizeGitHubLog(logText string) string {
	lower := strings.ToLower(logText)
	if strings.Contains(lower, "error") || strings.Contains(lower, "failed") || strings.Contains(lower, "failure") {
		return "GitHub CI logs contain failure indicators."
	}
	return "GitHub CI logs fetched without obvious failure indicators."
}

type githubCheckRunsResponse struct {
	TotalCount int `json:"total_count"`
	CheckRuns  []struct {
		Name       string `json:"name"`
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
		HTMLURL    string `json:"html_url"`
	} `json:"check_runs"`
}

type githubWorkflowRunsResponse struct {
	TotalCount   int `json:"total_count"`
	WorkflowRuns []struct {
		ID           int64  `json:"id"`
		Name         string `json:"name"`
		Status       string `json:"status"`
		Conclusion   string `json:"conclusion"`
		HTMLURL      string `json:"html_url"`
		HeadSHA      string `json:"head_sha"`
		HeadBranch   string `json:"head_branch"`
		Event        string `json:"event"`
		CreatedAt    string `json:"created_at"`
		UpdatedAt    string `json:"updated_at"`
		RunStartedAt string `json:"run_started_at"`
		WorkflowID   int64  `json:"workflow_id"`
	} `json:"workflow_runs"`
}

type githubWorkflowJobsResponse struct {
	TotalCount int `json:"total_count"`
	Jobs       []struct {
		ID         int64  `json:"id"`
		Name       string `json:"name"`
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
		HTMLURL    string `json:"html_url"`
		LogsURL    string `json:"logs_url"`
	} `json:"jobs"`
}

type githubPullResponse struct {
	Head struct {
		SHA string `json:"sha"`
	} `json:"head"`
}

type githubRefResponse struct {
	Object struct {
		SHA string `json:"sha"`
	} `json:"object"`
}

type githubContentResponse struct {
	SHA      string `json:"sha"`
	Content  string `json:"content"`
	Encoding string `json:"encoding"`
	HTMLURL  string `json:"html_url"`
}

type githubPullListItem struct {
	Number       int    `json:"number"`
	Title        string `json:"title"`
	HTMLURL      string `json:"html_url"`
	MergedAt     string `json:"merged_at"`
	UpdatedAt    string `json:"updated_at"`
	ChangedFiles int    `json:"changed_files"`
	User         struct {
		Login string `json:"login"`
	} `json:"user"`
}

type githubCreatePullResponse struct {
	Number  int    `json:"number"`
	Title   string `json:"title"`
	HTMLURL string `json:"html_url"`
	Draft   bool   `json:"draft"`
	Head    struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"head"`
}

type githubPullDetailResponse struct {
	Number         int    `json:"number"`
	Title          string `json:"title"`
	State          string `json:"state"`
	HTMLURL        string `json:"html_url"`
	Draft          bool   `json:"draft"`
	Merged         bool   `json:"merged"`
	MergedAt       string `json:"merged_at"`
	MergeCommitSHA string `json:"merge_commit_sha"`
	Head           struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
}

func (a GitHubAdapter) BaseURL() string {
	return a.baseURL
}

func (a GitHubAdapter) HTTPClient() *http.Client {
	return a.client
}

func GitHubProviderOverrides() map[string]string {
	return map[string]string{
		"code_host.get_recent_changes": GitHubProvider,
		"code_host.get_file":           GitHubProvider,
		"code_host.get_pull_request":   GitHubProvider,
		"code_host.create_draft_pr":    GitHubProvider,
		"ci.get_checks":                GitHubProvider,
		"ci.get_logs":                  GitHubProvider,
	}
}

func GitHubDeployProviderOverrides() map[string]string {
	return map[string]string{
		"deploy.get_recent_deploys": GitHubProvider,
	}
}
