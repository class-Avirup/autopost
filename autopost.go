package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/robfig/cron/v3"
)

type PromptResponse struct {
	Title       string                 `json:"title"`
	Description string                 `json:"description"`
	Prompt      string                 `json:"prompt"`
	UseCases    []string               `json:"useCases"`
	Example     map[string]interface{} `json:"example"`
	Tags        []string               `json:"tags"`
}

type rawPromptResponse struct {
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Prompt      string          `json:"prompt"`
	UseCases    []string        `json:"useCases"`
	Example     json.RawMessage `json:"example"`
	Tags        []string        `json:"tags"`
}

type GroqAPIResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

var (
	GroqAPIKey   string
	GroqEndpoint = "https://api.groq.com/openai/v1/chat/completions"
	BackendAPI   string
)

func main() {
	// Load environment variables from .env file
	err := godotenv.Load()
	if err != nil {
		log.Fatal("❌ Error loading .env file")
	}

	GroqAPIKey = os.Getenv("GROQ_API_KEY")
	BackendAPI = os.Getenv("BACKEND_API_URL")

	log.Println("🔐 GROQ_API_KEY loaded:", GroqAPIKey != "")
	log.Println("🔗 BACKEND_API:", BackendAPI)

	if GroqAPIKey == "" || BackendAPI == "" {
		log.Fatal("❌ Environment variables GROQ_API_KEY or BACKEND_API_URL not set")
	}

	log.Println("✅ Starting production cron job...")

	runPromptGeneration()

	c := cron.New()
	c.AddFunc("0 9 * * *", func() {
		log.Println("⏳ Scheduled prompt generation started...")
		runPromptGeneration()
	})
	c.Start()

	select {}
}

func runPromptGeneration() {
	prompt := `Generate an AI prompt that can be used by professionals in a specific industry. Randomly choose one of the following sectors: marketing, education, finance, healthcare, e-commerce, SaaS, real estate, coaching, or content creation.

Your task is to:
- Create a practical and high-quality AI prompt relevant to the selected sector
- Wrap your response in a clean JSON object with these keys:
  - "title": Short, engaging name of the AI prompt
  - "description": A brief explanation of what the AI prompt does and who it's for
  - "tags": 3 to 5 lowercase tags (e.g. "marketing", "ecommerce", "email")
  - "prompt": The actual AI prompt (what the user will copy and use)
  - "useCases": A list of 3–5 specific use cases for this prompt
  - "example": A single realistic example of the output when this prompt is used

Output your response ONLY as a JSON object, without any extra commentary or Markdown.`

	rawResponse, err := getPromptFromGroq(prompt)
	if err != nil {
		log.Println("❌ Failed to get prompt from Groq:", err)
		return
	}

	log.Println("📥 Raw Groq Response:\n", rawResponse)

	cleanedJSON := extractJSONBlock(rawResponse)
	log.Println("🧼 Cleaned JSON:\n", cleanedJSON)

	var raw rawPromptResponse
	if err := json.Unmarshal([]byte(cleanedJSON), &raw); err != nil {
		log.Printf("❌ Failed to parse Groq response.\nCleaned JSON:\n%s\nError: %v", cleanedJSON, err)
		return
	}

	var example map[string]interface{}
	if len(raw.Example) > 0 && raw.Example[0] == '{' {
		if err := json.Unmarshal(raw.Example, &example); err != nil {
			example = map[string]interface{}{"text": string(raw.Example)}
		}
	} else {
		example = map[string]interface{}{"text": string(raw.Example)}
	}

	structured := PromptResponse{
		Title:       raw.Title,
		Description: raw.Description,
		Prompt:      raw.Prompt,
		UseCases:    raw.UseCases,
		Tags:        raw.Tags,
		Example:     example,
	}

	if err := sendToBackend(structured); err != nil {
		log.Println("❌ Failed to send to backend:", err)
		return
	}

	log.Println("✅ Prompt saved successfully!")
}

func getPromptFromGroq(userPrompt string) (string, error) {
	requestBody := map[string]interface{}{
		"model": "llama3-70b-8192",
		"messages": []map[string]string{
			{
				"role":    "user",
				"content": userPrompt,
			},
		},
	}

	jsonBody, _ := json.Marshal(requestBody)

	req, err := http.NewRequest("POST", GroqEndpoint, bytes.NewBuffer(jsonBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+GroqAPIKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result GroqAPIResponse
	body, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("could not parse Groq API response: %w", err)
	}

	if len(result.Choices) == 0 {
		return "", fmt.Errorf("no choices returned from Groq")
	}

	return result.Choices[0].Message.Content, nil
}

func sendToBackend(prompt PromptResponse) error {
	payload := map[string]interface{}{
		"title":       prompt.Title,
		"description": prompt.Description,
		"tags":        prompt.Tags,
		"prompt":      prompt.Prompt,
		"useCases":    prompt.UseCases,
		"example":     prompt.Example,
		"createdAt":   time.Now(),
	}
	jsonPayload, _ := json.Marshal(payload)

	resp, err := http.Post(BackendAPI, "application/json", bytes.NewBuffer(jsonPayload))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("backend rejected data: %s", body)
	}
	return nil
}

func extractJSONBlock(text string) string {
	re := regexp.MustCompile(`(?s)\{.*\}`)
	match := re.FindString(text)

	match = regexp.MustCompile(`,\s*([\]}])`).ReplaceAllString(match, "$1")
	match = strings.TrimSpace(match)
	match = strings.TrimPrefix(match, "```json")
	match = strings.TrimSuffix(match, "```")

	return match
}
