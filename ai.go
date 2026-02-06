package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// AINode is the JSON structure exchanged with the AI model.
type AINode struct {
	Type          string   `json:"type"`                     // "group" or "task"
	ID            *int64   `json:"id,omitempty"`             // existing ID (update mode)
	TitleFR       string   `json:"title_fr"`
	TitleEN       string   `json:"title_en,omitempty"`
	DescriptionFR string   `json:"description_fr,omitempty"` // tasks only
	DescriptionEN string   `json:"description_en,omitempty"` // tasks only
	MaxSlots      *int64   `json:"max_slots,omitempty"`      // tasks only
	Children      []AINode `json:"children,omitempty"`        // groups only
}

// aiRequest is the JSON body the admin JS sends.
type aiRequest struct {
	EventID    int64  `json:"event_id"`
	Mode       string `json:"mode"` // "create" or "update"
	Text       string `json:"text"`
	DefaultOne bool   `json:"default_one"`
}

// callClaude sends a prompt to the Anthropic Messages API and returns the text response.
func callClaude(apiKey, systemPrompt, userPrompt string) (string, error) {
	body := map[string]any{
		"model":      "claude-sonnet-4-5-20250929",
		"max_tokens": 4096,
		"system":     systemPrompt,
		"messages": []map[string]string{
			{"role": "user", "content": userPrompt},
		},
	}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(jsonBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("API request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("parsing response: %w", err)
	}
	if len(result.Content) == 0 {
		return "", fmt.Errorf("empty response from API")
	}
	return result.Content[0].Text, nil
}

// parseAIResponse extracts JSON from the AI response (strips markdown fences if present).
func parseAIResponse(text string) ([]AINode, error) {
	s := strings.TrimSpace(text)
	// Strip markdown code fences
	if strings.HasPrefix(s, "```") {
		if idx := strings.Index(s[3:], "\n"); idx >= 0 {
			s = s[3+idx+1:]
		}
		if idx := strings.LastIndex(s, "```"); idx >= 0 {
			s = s[:idx]
		}
		s = strings.TrimSpace(s)
	}
	var nodes []AINode
	if err := json.Unmarshal([]byte(s), &nodes); err != nil {
		return nil, fmt.Errorf("invalid JSON from AI: %w\nRaw: %s", err, s[:min(len(s), 500)])
	}
	return nodes, nil
}

// treeToAINodes converts the current tree to AINode format for context in update mode.
func treeToAINodes(tree []TreeNode) []AINode {
	var nodes []AINode
	for _, n := range tree {
		switch n.Type {
		case "group":
			ai := AINode{
				Type:    "group",
				ID:      &n.Group.ID,
				TitleFR: n.Group.TitleFR,
				TitleEN: n.Group.TitleEN,
			}
			if len(n.Children) > 0 {
				ai.Children = treeToAINodes(n.Children)
			}
			nodes = append(nodes, ai)
		case "task":
			ai := AINode{
				Type:          "task",
				ID:            &n.Task.ID,
				TitleFR:       n.Task.TitleFR,
				TitleEN:       n.Task.TitleEN,
				DescriptionFR: n.Task.DescriptionFR,
				DescriptionEN: n.Task.DescriptionEN,
			}
			if n.Task.MaxSlots.Valid {
				v := n.Task.MaxSlots.Int64
				ai.MaxSlots = &v
			}
			nodes = append(nodes, ai)
		}
	}
	return nodes
}

const systemPrompt = `You are a helpful assistant that structures event volunteer tasks.
You receive text describing tasks/activities for a community event and must return a JSON array of groups and tasks.

Rules:
- Output ONLY valid JSON, no explanation, no markdown fences.
- The JSON is an array of objects, each with "type": "group" or "type": "task".
- Groups have: type, title_fr, title_en, children (array of nested groups/tasks).
- Tasks have: type, title_fr, title_en, description_fr (optional), description_en (optional), max_slots (integer or null).
- Translate between French and English as needed. If the input is in one language, provide both translations.
- Organize logically: use groups to categorize related tasks.
- If the text mentions a number of people needed, set max_slots accordingly.
- Keep titles concise and descriptions informative.
- Do NOT invent tasks not mentioned in the text.`

const updateSystemPrompt = `You are a helpful assistant that structures event volunteer tasks.
You receive the current task structure (as JSON with IDs) and new text instructions. You must return an updated JSON array.

Rules:
- Output ONLY valid JSON, no explanation, no markdown fences.
- The JSON is an array of objects, each with "type": "group" or "type": "task".
- Groups have: type, id (keep existing ID if updating), title_fr, title_en, children.
- Tasks have: type, id (keep existing ID if updating), title_fr, title_en, description_fr, description_en, max_slots.
- KEEP the "id" field for items that already exist and should be updated.
- OMIT the "id" field for brand new items to be created.
- Items from the current structure that are NOT in your output will be deleted.
- Translate between French and English as needed.
- Organize logically: use groups to categorize related tasks.
- If the text mentions a number of people needed, set max_slots accordingly.
- Do NOT invent tasks not mentioned or implied by the text.`

// applyAINodes recursively creates/updates groups and tasks from the AI output.
func applyAINodes(db *sql.DB, eventID int64, nodes []AINode, parentGroupID sql.NullInt64, position *int) error {
	for _, node := range nodes {
		pos := *position
		*position++

		switch node.Type {
		case "group":
			var groupID int64
			if node.ID != nil && *node.ID > 0 {
				// Update existing group
				if _, err := db.Exec("UPDATE task_groups SET title_fr=?, title_en=?, position=?, parent_group_id=? WHERE id=?",
					node.TitleFR, node.TitleEN, pos, parentGroupID, *node.ID); err != nil {
					return fmt.Errorf("updating group: %w", err)
				}
				groupID = *node.ID
			} else {
				// Create new group
				res, err := db.Exec("INSERT INTO task_groups (event_id, parent_group_id, title_fr, title_en, position) VALUES (?, ?, ?, ?, ?)",
					eventID, parentGroupID, node.TitleFR, node.TitleEN, pos)
				if err != nil {
					return fmt.Errorf("creating group: %w", err)
				}
				groupID, _ = res.LastInsertId()
			}
			// Recurse into children
			childPos := 0
			childParent := sql.NullInt64{Int64: groupID, Valid: true}
			if err := applyAINodes(db, eventID, node.Children, childParent, &childPos); err != nil {
				return err
			}

		case "task":
			if node.ID != nil && *node.ID > 0 {
				// Update existing task
				var maxSlots sql.NullInt64
				if node.MaxSlots != nil {
					maxSlots = sql.NullInt64{Int64: *node.MaxSlots, Valid: true}
				}
				if _, err := db.Exec("UPDATE tasks SET title_fr=?, title_en=?, description_fr=?, description_en=?, max_slots=?, position=?, group_id=? WHERE id=?",
					node.TitleFR, node.TitleEN, node.DescriptionFR, node.DescriptionEN, maxSlots, pos, parentGroupID, *node.ID); err != nil {
					return fmt.Errorf("updating task: %w", err)
				}
			} else {
				// Create new task
				var maxSlots sql.NullInt64
				if node.MaxSlots != nil {
					maxSlots = sql.NullInt64{Int64: *node.MaxSlots, Valid: true}
				}
				if _, err := db.Exec("INSERT INTO tasks (event_id, group_id, title_fr, title_en, description_fr, description_en, max_slots, position) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
					eventID, parentGroupID, node.TitleFR, node.TitleEN, node.DescriptionFR, node.DescriptionEN, maxSlots, pos); err != nil {
					return fmt.Errorf("creating task: %w", err)
				}
			}
		}
	}
	return nil
}

// collectExistingIDs gathers all group and task IDs from the AI response (for cleanup in update mode).
func collectExistingIDs(nodes []AINode) (groupIDs, taskIDs []int64) {
	for _, n := range nodes {
		if n.ID != nil && *n.ID > 0 {
			if n.Type == "group" {
				groupIDs = append(groupIDs, *n.ID)
			} else {
				taskIDs = append(taskIDs, *n.ID)
			}
		}
		if len(n.Children) > 0 {
			gids, tids := collectExistingIDs(n.Children)
			groupIDs = append(groupIDs, gids...)
			taskIDs = append(taskIDs, tids...)
		}
	}
	return
}

// handleAdminAIParse handles the AI text import endpoint.
func (app *App) handleAdminAIParse(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if app.AnthropicKey == "" {
		http.Error(w, "ANTHROPIC_API_KEY not configured", http.StatusServiceUnavailable)
		return
	}

	var req aiRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if req.Text == "" || req.EventID == 0 {
		http.Error(w, "text and event_id required", http.StatusBadRequest)
		return
	}

	var userPrompt string
	if req.Mode == "update" {
		// Build current tree context
		tree, err := BuildEventTree(app.DB, req.EventID)
		if err != nil {
			http.Error(w, "failed to load event tree", http.StatusInternalServerError)
			return
		}
		currentJSON, _ := json.MarshalIndent(treeToAINodes(tree), "", "  ")
		userPrompt = fmt.Sprintf("Current structure:\n%s\n\nInstructions:\n%s", string(currentJSON), req.Text)
	} else {
		userPrompt = req.Text
	}

	sysPrompt := systemPrompt
	if req.Mode == "update" {
		sysPrompt = updateSystemPrompt
	}
	if req.DefaultOne {
		sysPrompt += "\n- IMPORTANT: For tasks where no specific number of people is mentioned, set max_slots to 1."
	}

	response, err := callClaude(app.AnthropicKey, sysPrompt, userPrompt)
	if err != nil {
		http.Error(w, fmt.Sprintf("AI error: %v", err), http.StatusBadGateway)
		return
	}

	aiNodes, err := parseAIResponse(response)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to parse AI response: %v", err), http.StatusBadGateway)
		return
	}

	// In update mode, delete items that are no longer in the AI output
	if req.Mode == "update" {
		keepGroupIDs, keepTaskIDs := collectExistingIDs(aiNodes)

		// Delete tasks not in the keep list
		allTasks, _ := ListTasks(app.DB, req.EventID)
		for _, t := range allTasks {
			keep := false
			for _, kid := range keepTaskIDs {
				if t.ID == kid {
					keep = true
					break
				}
			}
			if !keep {
				DeleteTask(app.DB, t.ID)
			}
		}

		// Delete groups not in the keep list (children promoted by DeleteTaskGroup)
		allGroups, _ := ListTaskGroups(app.DB, req.EventID)
		for _, g := range allGroups {
			keep := false
			for _, kid := range keepGroupIDs {
				if g.ID == kid {
					keep = true
					break
				}
			}
			if !keep {
				DeleteTaskGroup(app.DB, g.ID)
			}
		}
	}

	// Apply the AI nodes (create/update)
	pos := 0
	if err := applyAINodes(app.DB, req.EventID, aiNodes, sql.NullInt64{}, &pos); err != nil {
		http.Error(w, fmt.Sprintf("Failed to apply changes: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
