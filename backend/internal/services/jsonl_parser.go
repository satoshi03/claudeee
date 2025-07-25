package services

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	
	"claudeee-backend/internal/models"
)

type JSONLParser struct {
	db             *sql.DB
	tokenService   *TokenService
	sessionService *SessionService
	windowService  *SessionWindowService
}

func NewJSONLParser(db *sql.DB, tokenService *TokenService, sessionService *SessionService) *JSONLParser {
	windowService := NewSessionWindowService(db)
	return &JSONLParser{
		db:             db,
		tokenService:   tokenService,
		sessionService: sessionService,
		windowService:  windowService,
	}
}


func (p *JSONLParser) SyncAllLogs() error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}
	
	claudeDir := filepath.Join(homeDir, ".claude", "projects")
	fmt.Printf("Looking for Claude projects in: %s\n", claudeDir)
	
	if _, err := os.Stat(claudeDir); os.IsNotExist(err) {
		return fmt.Errorf("claude projects directory not found: %s", claudeDir)
	}
	
	entries, err := os.ReadDir(claudeDir)
	if err != nil {
		return fmt.Errorf("failed to read claude projects directory: %w", err)
	}
	
	fmt.Printf("Found %d projects\n", len(entries))
	
	for _, entry := range entries {
		if entry.IsDir() {
			projectPath := filepath.Join(claudeDir, entry.Name())
			fmt.Printf("Syncing project: %s\n", entry.Name())
			if err := p.syncProjectLogs(projectPath, entry.Name()); err != nil {
				fmt.Printf("Error syncing project %s: %v\n", entry.Name(), err)
			}
		}
	}
	
	return nil
}

func (p *JSONLParser) syncProjectLogs(projectPath, projectName string) error {
	files, err := filepath.Glob(filepath.Join(projectPath, "*.jsonl"))
	if err != nil {
		return fmt.Errorf("failed to glob jsonl files: %w", err)
	}
	
	fmt.Printf("Found %d JSONL files in %s\n", len(files), projectPath)
	
	for _, file := range files {
		fmt.Printf("Processing file: %s\n", file)
		if err := p.parseJSONLFile(file, projectName); err != nil {
			fmt.Printf("Error parsing file %s: %v\n", file, err)
		}
	}
	
	return nil
}

func (p *JSONLParser) parseJSONLFile(filePath, projectName string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()
	
	fileName := filepath.Base(filePath)
	scanner := bufio.NewScanner(file)
	
	// Increase buffer size to handle very long lines (up to 10MB)
	const maxCapacity = 10 * 1024 * 1024 // 10MB
	buf := make([]byte, maxCapacity)
	scanner.Buffer(buf, maxCapacity)
	
	lineCount := 0
	processedCount := 0
	
	for scanner.Scan() {
		lineCount++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		
		var entry models.LogEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			fmt.Printf("Error unmarshaling line %d in file %s: %v\n", lineCount, fileName, err)
			fmt.Printf("Problematic JSON line: %s\n", line)
			continue
		}
		
		if err := p.processLogEntry(&entry, projectName); err != nil {
			fmt.Printf("Error processing log entry %d in file %s (UUID: %s, SessionID: %s): %v\n", lineCount, fileName, entry.UUID, entry.SessionID, err)
			fmt.Printf("Entry details: %+v\n", entry)
			continue
		}
		processedCount++
	}
	
	fmt.Printf("Processed %d/%d lines from %s\n", processedCount, lineCount, filePath)
	return scanner.Err()
}

func (p *JSONLParser) processLogEntry(entry *models.LogEntry, projectName string) error {
	// Use cwd from log entry if available, otherwise fall back to project name conversion
	var actualProjectPath, actualProjectName string
	if entry.Cwd != "" {
		actualProjectPath = entry.Cwd
		actualProjectName = p.extractProjectNameFromCwd(entry.Cwd)
	} else {
		actualProjectPath = p.convertProjectNameToPath(projectName)
		actualProjectName = projectName
	}
	
	if err := p.sessionService.CreateOrUpdateSession(entry.SessionID, actualProjectName, actualProjectPath, entry.Timestamp); err != nil {
		return fmt.Errorf("failed to create/update session: %w", err)
	}
	
	message := &models.Message{
		ID:          entry.UUID,
		SessionID:   entry.SessionID,
		ParentUUID:  entry.ParentUUID,
		IsSidechain: entry.IsSidechain,
		UserType:    &entry.UserType,
		MessageType: entry.Message.Type,
		MessageRole: &entry.Message.Role,
		Model:       entry.Message.Model,
		Timestamp:   entry.Timestamp,
		RequestID:   entry.RequestID,
	}
	
	if entry.Message.Content != nil {
		contentStr := p.convertContentToString(entry.Message.Content)
		message.Content = &contentStr
	}
	
	if entry.Message.Usage != nil {
		message.InputTokens = entry.Message.Usage.InputTokens
		message.CacheCreationInputTokens = entry.Message.Usage.CacheCreationInputTokens
		message.CacheReadInputTokens = entry.Message.Usage.CacheReadInputTokens
		message.OutputTokens = entry.Message.Usage.OutputTokens
		message.ServiceTier = &entry.Message.Usage.ServiceTier
	}
	
	// Get or create appropriate session window for this message
	window, err := p.windowService.GetOrCreateWindowForMessage(entry.Timestamp)
	if err != nil {
		return fmt.Errorf("failed to get/create session window: %w", err)
	}
	message.SessionWindowID = &window.ID
	
	if err := p.insertMessage(message); err != nil {
		return fmt.Errorf("failed to insert message: %w", err)
	}

	// Update window statistics after message insertion
	if err := p.windowService.UpdateWindowStats(window.ID); err != nil {
		return fmt.Errorf("failed to update window stats: %w", err)
	}
	
	if err := p.tokenService.UpdateSessionTokens(entry.SessionID); err != nil {
		return fmt.Errorf("failed to update session tokens: %w", err)
	}
	
	return nil
}

func (p *JSONLParser) insertMessage(message *models.Message) error {
	// Use INSERT OR REPLACE to handle both insert and update cases
	upsertQuery := `
		INSERT OR REPLACE INTO messages (
			id, session_id, session_window_id, parent_uuid, is_sidechain, user_type, message_type,
			message_role, model, content, input_tokens, cache_creation_input_tokens,
			cache_read_input_tokens, output_tokens, service_tier, request_id,
			timestamp, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	
	_, err := p.db.Exec(upsertQuery,
		message.ID,
		message.SessionID,
		message.SessionWindowID,
		message.ParentUUID,
		message.IsSidechain,
		message.UserType,
		message.MessageType,
		message.MessageRole,
		message.Model,
		message.Content,
		message.InputTokens,
		message.CacheCreationInputTokens,
		message.CacheReadInputTokens,
		message.OutputTokens,
		message.ServiceTier,
		message.RequestID,
		message.Timestamp,
		time.Now(),
	)
	if err != nil {
		return fmt.Errorf("failed to upsert message: %w", err)
	}
	
	// Log successful upsert for debugging
	if message.MessageRole != nil && *message.MessageRole == "assistant" {
		fmt.Printf("Upserted assistant message: ID=%s, SessionID=%s, Timestamp=%s, Tokens=%d\n", 
			message.ID, message.SessionID, message.Timestamp.Format(time.RFC3339), message.InputTokens+message.OutputTokens)
	}
	return nil
}

func (p *JSONLParser) convertProjectNameToPath(projectName string) string {
	if strings.HasPrefix(projectName, "-") {
		return "/" + strings.ReplaceAll(projectName[1:], "-", "/")
	}
	return projectName
}

// extractProjectNameFromCwd extracts a meaningful project name from the cwd path
func (p *JSONLParser) extractProjectNameFromCwd(cwd string) string {
	if cwd == "" {
		return "unknown"
	}
	
	// Clean the path and get the last meaningful component
	cleanPath := filepath.Clean(cwd)
	
	// Split the path into components
	parts := strings.Split(cleanPath, "/")
	if len(parts) == 0 {
		return "unknown"
	}
	
	// Look for common subdirectory patterns that indicate we're in a project subdirectory
	commonSubdirs := []string{"frontend", "backend", "src", "lib"}
	
	// Find the last non-empty part that's meaningful as a project name
	for i := len(parts) - 1; i >= 0; i-- {
		part := parts[i]
		if part != "" && part != "." && part != ".." {
			// Check if this part is a common subdirectory
			isCommonSubdir := false
			for _, subdir := range commonSubdirs {
				if part == subdir {
					isCommonSubdir = true
					break
				}
			}
			
			if isCommonSubdir {
				// Look for the parent directory that might be the project name
				for j := i - 1; j >= 0; j-- {
					parentPart := parts[j]
					if parentPart != "" && parentPart != "." && parentPart != ".." {
						// Check if this parent is also a common subdirectory
						isParentCommonSubdir := false
						for _, subdir := range commonSubdirs {
							if parentPart == subdir {
								isParentCommonSubdir = true
								break
							}
						}
						
						if !isParentCommonSubdir {
							return parentPart
						}
					}
				}
			}
			return part
		}
	}
	
	return "unknown"
}

func (p *JSONLParser) convertContentToString(content interface{}) string {
	switch v := content.(type) {
	case string:
		return v
	case map[string]interface{}:
		data, _ := json.Marshal(v)
		return string(data)
	case []interface{}:
		data, _ := json.Marshal(v)
		return string(data)
	default:
		return fmt.Sprintf("%v", v)
	}
}