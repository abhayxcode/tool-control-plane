package controlplane

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const KubernetesProvider = "kubernetes"

const kubernetesLogExcerptLimit = 4000

type KubernetesAdapterConfig struct {
	BaseURL          string
	BearerToken      string
	Namespace        string
	LabelSelector    string
	ServiceLabel     string
	EnvironmentLabel string
	Client           *http.Client
}

type KubernetesAdapter struct {
	baseURL          string
	bearerToken      string
	namespace        string
	labelSelector    string
	serviceLabel     string
	environmentLabel string
	client           *http.Client
}

func NewKubernetesAdapter(config KubernetesAdapterConfig) KubernetesAdapter {
	client := config.Client
	if client == nil {
		client = http.DefaultClient
	}
	return KubernetesAdapter{
		baseURL:          strings.TrimRight(strings.TrimSpace(config.BaseURL), "/"),
		bearerToken:      strings.TrimSpace(config.BearerToken),
		namespace:        firstNonEmpty(config.Namespace, "default"),
		labelSelector:    strings.TrimSpace(config.LabelSelector),
		serviceLabel:     firstNonEmpty(config.ServiceLabel, "app"),
		environmentLabel: strings.TrimSpace(config.EnvironmentLabel),
		client:           client,
	}
}

func (a KubernetesAdapter) Execute(definition CapabilityDefinition, req ToolCallRequest) (map[string]any, error) {
	if strings.TrimSpace(a.baseURL) == "" {
		return nil, errors.New("kubernetes adapter requires KUBERNETES_BASE_URL")
	}
	if definition.ID != "runtime.get_workload_status" {
		return nil, fmt.Errorf("kubernetes adapter does not support capability '%s'", definition.ID)
	}
	return a.getWorkloadStatus(req)
}

func (a KubernetesAdapter) getWorkloadStatus(req ToolCallRequest) (map[string]any, error) {
	args := req.Arguments
	namespace := firstNonEmpty(firstStringArg(args, "namespace"), a.namespace, "default")
	workload := firstNonEmpty(firstStringArg(args, "workload", "service", "service_id"), req.ServiceID)
	environment := firstNonEmpty(firstStringArg(args, "environment", "env"), req.Environment)
	labelSelector := firstNonEmpty(firstStringArg(args, "label_selector", "labelSelector"), a.labelSelector, a.selectorForWorkload(workload, environment))
	podLimit := boundedInt(optionalIntArg(args, "pod_limit", 10), 1, 20)
	eventLimit := boundedInt(optionalIntArg(args, "event_limit", 20), 1, 50)
	logPodLimit := boundedInt(optionalIntArg(args, "log_pod_limit", 3), 1, 5)
	tailLines := boundedInt(optionalIntArg(args, "tail_lines", 50), 1, 200)
	includeLogs := optionalBoolArg(args, "include_logs", true)

	pods, err := a.listPods(namespace, labelSelector, podLimit)
	if err != nil {
		return nil, err
	}
	podNames := map[string]bool{}
	podSummaries := make([]map[string]any, 0, len(pods.Items))
	totalRestarts := 0
	readyCount := 0
	unhealthyPods := []kubernetesPod{}
	for _, pod := range pods.Items {
		podNames[pod.Metadata.Name] = true
		summary := summarizeKubernetesPod(pod)
		podSummaries = append(podSummaries, summary)
		restarts := podRestartCount(pod)
		totalRestarts += restarts
		if ready, _ := summary["ready"].(bool); ready {
			readyCount++
		} else {
			unhealthyPods = append(unhealthyPods, pod)
		}
		if restarts > 0 && podReady(pod) {
			unhealthyPods = append(unhealthyPods, pod)
		}
	}

	warnings := []string{}
	events, eventErr := a.listPodEvents(namespace, podNames, eventLimit)
	if eventErr != nil {
		warnings = append(warnings, eventErr.Error())
	}
	warningEvents := 0
	for _, event := range events {
		if strings.EqualFold(event["type"].(string), "Warning") {
			warningEvents++
		}
	}

	logs := []map[string]any{}
	if includeLogs && len(unhealthyPods) > 0 {
		seen := map[string]bool{}
		for _, pod := range unhealthyPods {
			if len(logs) >= logPodLimit {
				break
			}
			if seen[pod.Metadata.Name] {
				continue
			}
			seen[pod.Metadata.Name] = true
			logText, err := a.podLog(namespace, pod.Metadata.Name, tailLines)
			if err != nil {
				warnings = append(warnings, err.Error())
				continue
			}
			excerpt, truncated := boundedText(logText, kubernetesLogExcerptLimit)
			logs = append(logs, map[string]any{
				"pod":         pod.Metadata.Name,
				"log_excerpt": excerpt,
				"truncated":   truncated,
			})
		}
	}

	status := kubernetesWorkloadStatus(len(pods.Items), readyCount, totalRestarts, warningEvents)
	result := map[string]any{
		"status":              status,
		"namespace":           namespace,
		"workload":            workload,
		"label_selector":      labelSelector,
		"pods_ready":          fmt.Sprintf("%d/%d", readyCount, len(pods.Items)),
		"ready_pods":          readyCount,
		"total_pods":          len(pods.Items),
		"restart_count":       totalRestarts,
		"warning_event_count": warningEvents,
		"pods":                podSummaries,
		"events":              events,
		"logs":                logs,
		"source_url":          a.podsURL(namespace, labelSelector, podLimit),
		"evidence":            kubernetesEvidence(namespace, workload, status, readyCount, len(pods.Items), totalRestarts, warningEvents),
	}
	if len(warnings) > 0 {
		result["warnings"] = warnings
	}
	return result, nil
}

func (a KubernetesAdapter) selectorForWorkload(workload string, environment string) string {
	matchers := []string{}
	if strings.TrimSpace(workload) != "" {
		matchers = append(matchers, a.serviceLabel+"="+workload)
	}
	if strings.TrimSpace(a.environmentLabel) != "" && strings.TrimSpace(environment) != "" {
		matchers = append(matchers, a.environmentLabel+"="+environment)
	}
	return strings.Join(matchers, ",")
}

func (a KubernetesAdapter) listPods(namespace string, labelSelector string, limit int) (kubernetesPodList, error) {
	query := url.Values{}
	if strings.TrimSpace(labelSelector) != "" {
		query.Set("labelSelector", labelSelector)
	}
	query.Set("limit", strconv.Itoa(limit))
	var pods kubernetesPodList
	err := a.getJSON(a.namespacedPath(namespace, "pods"), query, &pods)
	return pods, err
}

func (a KubernetesAdapter) listPodEvents(namespace string, podNames map[string]bool, limit int) ([]map[string]any, error) {
	if len(podNames) == 0 {
		return []map[string]any{}, nil
	}
	query := url.Values{}
	query.Set("fieldSelector", "involvedObject.kind=Pod")
	query.Set("limit", strconv.Itoa(limit))
	var events kubernetesEventList
	if err := a.getJSON(a.namespacedPath(namespace, "events"), query, &events); err != nil {
		return nil, err
	}
	result := []map[string]any{}
	for _, event := range events.Items {
		if !podNames[event.InvolvedObject.Name] {
			continue
		}
		result = append(result, map[string]any{
			"pod":       event.InvolvedObject.Name,
			"type":      event.Type,
			"reason":    event.Reason,
			"message":   event.Message,
			"count":     event.Count,
			"timestamp": firstNonEmpty(event.LastTimestamp, event.EventTime, event.FirstTimestamp, event.Metadata.CreationTimestamp),
		})
		if len(result) >= limit {
			break
		}
	}
	return result, nil
}

func (a KubernetesAdapter) podLog(namespace string, podName string, tailLines int) (string, error) {
	query := url.Values{}
	query.Set("tailLines", strconv.Itoa(tailLines))
	query.Set("timestamps", "true")
	return a.getText(a.namespacedPath(namespace, "pods", podName, "log"), query)
}

func (a KubernetesAdapter) getJSON(path string, query url.Values, out any) error {
	body, err := a.doRequest(path, query, "application/json")
	if err != nil {
		return err
	}
	return json.Unmarshal(body, out)
}

func (a KubernetesAdapter) getText(path string, query url.Values) (string, error) {
	body, err := a.doRequest(path, query, "text/plain")
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func (a KubernetesAdapter) doRequest(path string, query url.Values, accept string) ([]byte, error) {
	requestURL := a.baseURL + path
	if len(query) > 0 {
		requestURL += "?" + query.Encode()
	}
	req, err := http.NewRequest(http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, err
	}
	if a.bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+a.bearerToken)
	}
	req.Header.Set("Accept", accept)
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, newKubernetesRequestError(http.MethodGet, requestURL, 0, nil, err)
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, readErr
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, newKubernetesRequestError(http.MethodGet, requestURL, resp.StatusCode, body, nil)
	}
	return body, nil
}

func (a KubernetesAdapter) namespacedPath(namespace string, parts ...string) string {
	escaped := []string{"/api/v1/namespaces", url.PathEscape(namespace)}
	for _, part := range parts {
		escaped = append(escaped, url.PathEscape(part))
	}
	return strings.Join(escaped, "/")
}

func (a KubernetesAdapter) podsURL(namespace string, labelSelector string, limit int) string {
	query := url.Values{}
	if strings.TrimSpace(labelSelector) != "" {
		query.Set("labelSelector", labelSelector)
	}
	query.Set("limit", strconv.Itoa(limit))
	return a.baseURL + a.namespacedPath(namespace, "pods") + "?" + query.Encode()
}

type kubernetesPodList struct {
	Items []kubernetesPod `json:"items"`
}

type kubernetesPod struct {
	Metadata struct {
		Name              string            `json:"name"`
		Namespace         string            `json:"namespace"`
		Labels            map[string]string `json:"labels"`
		CreationTimestamp string            `json:"creationTimestamp"`
	} `json:"metadata"`
	Status struct {
		Phase             string                      `json:"phase"`
		Reason            string                      `json:"reason"`
		Message           string                      `json:"message"`
		PodIP             string                      `json:"podIP"`
		HostIP            string                      `json:"hostIP"`
		StartTime         string                      `json:"startTime"`
		Conditions        []kubernetesPodCondition    `json:"conditions"`
		ContainerStatuses []kubernetesContainerStatus `json:"containerStatuses"`
		InitStatuses      []kubernetesContainerStatus `json:"initContainerStatuses"`
		EphemeralStatuses []kubernetesContainerStatus `json:"ephemeralContainerStatuses"`
	} `json:"status"`
	Spec struct {
		NodeName string `json:"nodeName"`
	} `json:"spec"`
}

type kubernetesPodCondition struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

type kubernetesContainerStatus struct {
	Name                 string                   `json:"name"`
	Ready                bool                     `json:"ready"`
	RestartCount         int                      `json:"restartCount"`
	State                kubernetesContainerState `json:"state"`
	LastTerminationState kubernetesContainerState `json:"lastState"`
}

type kubernetesContainerState struct {
	Waiting    *kubernetesContainerWaiting    `json:"waiting,omitempty"`
	Running    *kubernetesContainerRunning    `json:"running,omitempty"`
	Terminated *kubernetesContainerTerminated `json:"terminated,omitempty"`
}

type kubernetesContainerWaiting struct {
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

type kubernetesContainerRunning struct {
	StartedAt string `json:"startedAt"`
}

type kubernetesContainerTerminated struct {
	ExitCode   int    `json:"exitCode"`
	Reason     string `json:"reason"`
	Message    string `json:"message"`
	StartedAt  string `json:"startedAt"`
	FinishedAt string `json:"finishedAt"`
}

type kubernetesEventList struct {
	Items []kubernetesEvent `json:"items"`
}

type kubernetesEvent struct {
	Metadata struct {
		Name              string `json:"name"`
		CreationTimestamp string `json:"creationTimestamp"`
	} `json:"metadata"`
	InvolvedObject struct {
		Kind string `json:"kind"`
		Name string `json:"name"`
	} `json:"involvedObject"`
	Type           string `json:"type"`
	Reason         string `json:"reason"`
	Message        string `json:"message"`
	Count          int    `json:"count"`
	FirstTimestamp string `json:"firstTimestamp"`
	LastTimestamp  string `json:"lastTimestamp"`
	EventTime      string `json:"eventTime"`
}

func summarizeKubernetesPod(pod kubernetesPod) map[string]any {
	ready := podReady(pod)
	return map[string]any{
		"name":           pod.Metadata.Name,
		"namespace":      pod.Metadata.Namespace,
		"phase":          pod.Status.Phase,
		"ready":          ready,
		"node":           pod.Spec.NodeName,
		"pod_ip":         pod.Status.PodIP,
		"host_ip":        pod.Status.HostIP,
		"restart_count":  podRestartCount(pod),
		"waiting_reason": firstNonEmpty(containerWaitingReason(pod.Status.ContainerStatuses), containerWaitingReason(pod.Status.InitStatuses), containerWaitingReason(pod.Status.EphemeralStatuses)),
		"message":        firstNonEmpty(pod.Status.Message, containerWaitingMessage(pod.Status.ContainerStatuses), containerWaitingMessage(pod.Status.InitStatuses), containerWaitingMessage(pod.Status.EphemeralStatuses)),
		"started_at":     pod.Status.StartTime,
	}
}

func podReady(pod kubernetesPod) bool {
	if pod.Status.Phase != "Running" {
		return false
	}
	for _, condition := range pod.Status.Conditions {
		if condition.Type == "Ready" {
			return condition.Status == "True"
		}
	}
	return false
}

func podRestartCount(pod kubernetesPod) int {
	total := 0
	for _, status := range pod.Status.ContainerStatuses {
		total += status.RestartCount
	}
	for _, status := range pod.Status.InitStatuses {
		total += status.RestartCount
	}
	for _, status := range pod.Status.EphemeralStatuses {
		total += status.RestartCount
	}
	return total
}

func containerWaitingReason(statuses []kubernetesContainerStatus) string {
	for _, status := range statuses {
		if status.State.Waiting != nil && strings.TrimSpace(status.State.Waiting.Reason) != "" {
			return status.State.Waiting.Reason
		}
		if status.State.Terminated != nil && strings.TrimSpace(status.State.Terminated.Reason) != "" {
			return status.State.Terminated.Reason
		}
		if status.LastTerminationState.Terminated != nil && strings.TrimSpace(status.LastTerminationState.Terminated.Reason) != "" {
			return status.LastTerminationState.Terminated.Reason
		}
	}
	return ""
}

func containerWaitingMessage(statuses []kubernetesContainerStatus) string {
	for _, status := range statuses {
		if status.State.Waiting != nil && strings.TrimSpace(status.State.Waiting.Message) != "" {
			return status.State.Waiting.Message
		}
		if status.State.Terminated != nil && strings.TrimSpace(status.State.Terminated.Message) != "" {
			return status.State.Terminated.Message
		}
		if status.LastTerminationState.Terminated != nil && strings.TrimSpace(status.LastTerminationState.Terminated.Message) != "" {
			return status.LastTerminationState.Terminated.Message
		}
	}
	return ""
}

func kubernetesWorkloadStatus(totalPods int, readyPods int, restarts int, warningEvents int) string {
	if totalPods == 0 {
		return "unknown"
	}
	if readyPods < totalPods || restarts > 0 || warningEvents > 0 {
		return "degraded"
	}
	return "healthy"
}

func kubernetesEvidence(namespace string, workload string, status string, readyPods int, totalPods int, restarts int, warningEvents int) string {
	if totalPods == 0 {
		return fmt.Sprintf("Kubernetes returned no pods for %s in namespace %s.", workload, namespace)
	}
	return fmt.Sprintf("Kubernetes reports %s for %s in namespace %s: %d/%d pods ready, %d container restart(s), %d warning event(s).", status, workload, namespace, readyPods, totalPods, restarts, warningEvents)
}

func boundedInt(value int, min int, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

type kubernetesRequestError struct {
	Method     string
	URL        string
	StatusCode int
	Body       string
	Cause      error
}

func newKubernetesRequestError(method string, requestURL string, statusCode int, body []byte, cause error) kubernetesRequestError {
	return kubernetesRequestError{
		Method:     method,
		URL:        requestURL,
		StatusCode: statusCode,
		Body:       strings.TrimSpace(string(body)),
		Cause:      cause,
	}
}

func (e kubernetesRequestError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("kubernetes API %s %s failed: %v", e.Method, e.URL, e.Cause)
	}
	if e.Body != "" {
		return fmt.Sprintf("kubernetes API %s %s failed with status %d: %s", e.Method, e.URL, e.StatusCode, e.Body)
	}
	return fmt.Sprintf("kubernetes API %s %s failed with status %d", e.Method, e.URL, e.StatusCode)
}

func (e kubernetesRequestError) Unwrap() error {
	return e.Cause
}

func (e kubernetesRequestError) ToolCallError() ToolCallError {
	return ToolCallError{
		Provider:   KubernetesProvider,
		Category:   "kubernetes_api",
		Operation:  e.Method,
		StatusCode: e.StatusCode,
		Message:    e.Error(),
	}
}

func KubernetesProviderOverrides() map[string]string {
	return map[string]string{
		"runtime.get_workload_status": KubernetesProvider,
	}
}
