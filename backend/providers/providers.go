package providers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"

	"github.com/semanser/ai-coder/database"

	"github.com/invopop/jsonschema"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/schema"
)

type ProviderType string

const (
	ProviderOpenAI ProviderType = "openai"
	ProviderOllama ProviderType = "ollama"
)

type Provider interface {
	New() Provider
	Name() ProviderType
	Summary(query string, n int) (string, error)
	DockerImageName(task string) (string, error)
	NextTask(args NextTaskOptions) *database.Task
}

type NextTaskOptions struct {
	Tasks       []database.Task
	DockerImage string
}

var Tools = []llms.Tool{
	{
		Type: "function",
		Function: &llms.FunctionDefinition{
			Name:        "terminal",
			Description: "Calls a terminal command",
			Parameters:  jsonschema.Reflect(&TerminalArgs{}).Definitions["TerminalArgs"],
		},
	},
	{
		Type: "function",
		Function: &llms.FunctionDefinition{
			Name:        "browser",
			Description: "Opens a browser to look for additional information",
			Parameters:  jsonschema.Reflect(&BrowserArgs{}).Definitions["BrowserArgs"],
		},
	},
	{
		Type: "function",
		Function: &llms.FunctionDefinition{
			Name:        "code",
			Description: "Modifies or reads code files",
			Parameters:  jsonschema.Reflect(&CodeArgs{}).Definitions["CodeArgs"],
		},
	},
	{
		Type: "function",
		Function: &llms.FunctionDefinition{
			Name:        "ask",
			Description: "Sends a question to the user for additional information",
			Parameters:  jsonschema.Reflect(&AskArgs{}).Definitions["AskArgs"],
		},
	},
	{
		Type: "function",
		Function: &llms.FunctionDefinition{
			Name:        "done",
			Description: "Mark the whole task as done. Should be called at the very end when everything is completed",
			Parameters:  jsonschema.Reflect(&DoneArgs{}).Definitions["DoneArgs"],
		},
	},
}

func ProviderFactory(provider ProviderType) (Provider, error) {
	switch provider {
	case ProviderOpenAI:
		return OpenAIProvider{}.New(), nil
	case ProviderOllama:
		return OllamaProvider{}.New(), nil
	default:
		return nil, fmt.Errorf("unknown provider: %s", provider)
	}
}

func defaultAskTask(message string) *database.Task {
	task := database.Task{
		Type: database.StringToNullString("ask"),
	}

	task.Args = database.StringToNullString("{}")
	task.Message = sql.NullString{
		String: fmt.Sprintf("%s. What should I do next?", message),
		Valid:  true,
	}

	return &task
}

func tasksToMessages(tasks []database.Task, prompt string) []llms.MessageContent {
	var messages []llms.MessageContent
	messages = append(messages, llms.MessageContent{
		Role: schema.ChatMessageTypeSystem,
		Parts: []llms.ContentPart{
			llms.TextPart(prompt),
		},
	})

	for _, task := range tasks {
		if task.Type.String == "input" {
			messages = append(messages, llms.MessageContent{
				Role: schema.ChatMessageTypeHuman,
				Parts: []llms.ContentPart{
					llms.TextPart(prompt),
				},
			})
		}

		if task.ToolCallID.String != "" {
			messages = append(messages, llms.MessageContent{
				Role: schema.ChatMessageTypeAI,
				Parts: []llms.ContentPart{
					llms.ToolCall{
						ID: task.ToolCallID.String,
						FunctionCall: &schema.FunctionCall{
							Name:      task.Type.String,
							Arguments: task.Args.String,
						},
						Type: "function",
					},
				},
			})

			messages = append(messages, llms.MessageContent{
				Role: schema.ChatMessageTypeTool,
				Parts: []llms.ContentPart{
					llms.ToolCallResponse{
						ToolCallID: task.ToolCallID.String,
						Name:       task.Type.String,
						Content:    task.Results.String,
					},
				},
			})
		}

		// This Ask was generated by the agent itself in case of some error (not the OpenAI)
		if task.Type.String == "ask" && task.ToolCallID.String == "" {
			messages = append(messages, llms.MessageContent{
				Role: schema.ChatMessageTypeAI,
				Parts: []llms.ContentPart{
					llms.TextPart(task.Message.String),
				},
			})
		}
	}

	return messages
}

func textToTask(text string) (*database.Task, error) {
	c := unmarshalCall(text)

	if c == nil {
		return nil, fmt.Errorf("can't unmarshalCall %s", text)
	}

	task := database.Task{
		// TODO validate tool name
		Type: database.StringToNullString(c.Tool),
	}

	arg, err := json.Marshal(c.Input)
	if err != nil {
		log.Printf("Failed to marshal terminal args, asking user: %v", err)
		return defaultAskTask("There was an error running the terminal command"), nil
	}
	task.Args = database.StringToNullString(string(arg))

	// Sometimes the model returns an empty string for the message
	// In that case, we use the input as the message
	msg := c.Message
	if msg == "" {
		msg = string(arg)
	}

	task.Message = database.StringToNullString(msg)
	task.Status = database.StringToNullString("in_progress")

	return &task, nil
}

func extractJSONArgs[T any](functionArgs map[string]string, args *T) (*T, error) {
	b, err := json.Marshal(functionArgs)

	if err != nil {
		return nil, fmt.Errorf("failed to marshal args: %v", err)
	}

	err = json.Unmarshal(b, args)

	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal args: %v", err)
	}
	return args, nil
}

func unmarshalCall(input string) *Call {
	log.Printf("Unmarshalling tool call: %v", input)

	var c Call

	err := json.Unmarshal([]byte(input), &c)
	if err != nil {
		log.Printf("Failed to unmarshal tool call: %v", err)
		return nil
	}

	if c.Tool != "" {
		log.Printf("Unmarshalled tool call: %v", c)
		return &c
	}

	return nil
}

func toolToTask(choices []*llms.ContentChoice) (*database.Task, error) {
	if len(choices) == 0 {
		return nil, fmt.Errorf("no choices found, asking user")
	}

	toolCalls := choices[0].ToolCalls

	if len(toolCalls) == 0 {
		return nil, fmt.Errorf("no tool calls found, asking user")
	}

	tool := toolCalls[0]

	task := database.Task{
		Type: database.StringToNullString(tool.FunctionCall.Name),
	}

	if tool.FunctionCall.Name == "" {
		return nil, fmt.Errorf("no tool name found, asking user")
	}

	// We use AskArgs to extract the message
	params, err := extractToolArgs(tool.FunctionCall.Arguments, &AskArgs{})
	if err != nil {
		return nil, fmt.Errorf("failed to extract args: %v", err)
	}
	args, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal terminal args, asking user: %v", err)
	}
	task.Args = database.StringToNullString(string(args))

	// Sometimes the model returns an empty string for the message
	msg := string(params.Message)
	if msg == "" {
		msg = tool.FunctionCall.Arguments
	}

	task.Message = database.StringToNullString(msg)
	task.Status = database.StringToNullString("in_progress")

	task.ToolCallID = database.StringToNullString(tool.ID)

	return &task, nil
}

func extractToolArgs[T any](functionArgs string, args *T) (*T, error) {
	err := json.Unmarshal([]byte(functionArgs), args)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal args: %v", err)
	}
	return args, nil
}
