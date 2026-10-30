package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Task struct {
	ID          int       `json:"id"`
	Description string    `json:"description"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}
type Store struct {
	NextID int    `json:"nextId"`
	Tasks  []Task `json:"tasks"`
}

const usage = `Usage:
  task-cli add "description"
  task-cli update <id> "description"
  task-cli delete <id>
  task-cli mark-in-progress <id>
  task-cli mark-done <id>
  task-cli list [todo|in-progress|done|not-done]
  task-cli help

not-done includes todo and in-progress. Storage: ./tasks.json`

func main() {
	if err := run(os.Args[1:], "tasks.json", os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
func validStatus(s string) bool { return s == "todo" || s == "in-progress" || s == "done" }
func run(args []string, path string, out io.Writer) error {
	if len(args) == 0 || (len(args) == 1 && (args[0] == "help" || args[0] == "--help" || args[0] == "-h")) {
		_, err := fmt.Fprintln(out, usage)
		return err
	}
	cmd := args[0]
	want := 2
	switch cmd {
	case "add", "delete", "mark-in-progress", "mark-done":
	case "update":
		want = 3
	case "list":
		if len(args) != 1 && len(args) != 2 {
			return errors.New("usage: task-cli list [todo|in-progress|done|not-done]")
		}
		want = len(args)
	default:
		return fmt.Errorf("unknown command %q; run task-cli help", cmd)
	}
	if len(args) != want {
		return fmt.Errorf("invalid arguments for %s; run task-cli help", cmd)
	}
	filter := ""
	if cmd == "list" && len(args) == 2 {
		filter = args[1]
		if !validStatus(filter) && filter != "not-done" {
			return fmt.Errorf("unknown status %q", filter)
		}
	}
	id := 0
	if cmd != "add" && cmd != "list" {
		var err error
		id, err = strconv.Atoi(args[1])
		if err != nil || id < 1 {
			return errors.New("task ID must be a positive integer")
		}
	}
	description := ""
	if cmd == "add" || cmd == "update" {
		description = strings.TrimSpace(args[len(args)-1])
		if description == "" {
			return errors.New("task description must not be empty")
		}
	}
	store, err := load(path)
	if err != nil {
		return err
	}
	if cmd == "list" {
		count := 0
		for _, task := range store.Tasks {
			if filter != "" && task.Status != filter && !(filter == "not-done" && task.Status != "done") {
				continue
			}
			fmt.Fprintf(out, "%d. [%s] %s\n", task.ID, task.Status, task.Description)
			count++
		}
		if count == 0 {
			fmt.Fprintln(out, "No tasks found.")
		}
		return nil
	}
	now := time.Now().UTC()
	message := ""
	if cmd == "add" {
		id = store.NextID
		store.NextID++
		store.Tasks = append(store.Tasks, Task{id, description, "todo", now, now})
		message = fmt.Sprintf("Task added (ID: %d).", id)
	} else {
		index := -1
		for i := range store.Tasks {
			if store.Tasks[i].ID == id {
				index = i
				break
			}
		}
		if index < 0 {
			return fmt.Errorf("task %d does not exist", id)
		}
		switch cmd {
		case "delete":
			store.Tasks = append(store.Tasks[:index], store.Tasks[index+1:]...)
			message = fmt.Sprintf("Task %d deleted.", id)
		case "update":
			store.Tasks[index].Description = description
			store.Tasks[index].UpdatedAt = now
			message = fmt.Sprintf("Task %d updated.", id)
		default:
			status := "in-progress"
			if cmd == "mark-done" {
				status = "done"
			}
			store.Tasks[index].Status = status
			store.Tasks[index].UpdatedAt = now
			message = fmt.Sprintf("Task %d marked %s.", id, status)
		}
	}
	if err := save(path, store); err != nil {
		return err
	}
	fmt.Fprintln(out, message)
	return nil
}
func load(path string) (Store, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		store := Store{NextID: 1, Tasks: []Task{}}
		return store, save(path, store)
	}
	var store Store
	if err != nil {
		return store, fmt.Errorf("read tasks: %w", err)
	}
	if err = json.Unmarshal(data, &store); err != nil {
		return store, fmt.Errorf("invalid JSON (file left unchanged): %w", err)
	}
	if store.NextID < 1 || store.Tasks == nil {
		return store, errors.New("invalid tasks file structure (file left unchanged)")
	}
	seen := map[int]bool{}
	for _, task := range store.Tasks {
		if task.ID < 1 || task.ID >= store.NextID || seen[task.ID] || strings.TrimSpace(task.Description) == "" || !validStatus(task.Status) || task.CreatedAt.IsZero() || task.UpdatedAt.Before(task.CreatedAt) {
			return store, errors.New("invalid task data (file left unchanged)")
		}
		seen[task.ID] = true
	}
	return store, nil
}

// Rename only after the complete JSON has been written successfully.
func save(path string, store Store) error {
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".tasks-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(append(data, '\n')); err != nil {
		file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	if err = os.Rename(file.Name(), path); err != nil {
		return fmt.Errorf("save tasks: %w", err)
	}
	return nil
}
