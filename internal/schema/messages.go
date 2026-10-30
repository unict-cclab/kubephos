package schema

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var issuePathPart = regexp.MustCompile(`[^.\[\]]+`)
var fieldWordBoundary = regexp.MustCompile(`([a-z0-9])([A-Z])`)

func DescribeIssues(definition json.RawMessage, issues []Issue) []Issue {
	result := make([]Issue, len(issues))
	for index, issue := range issues {
		result[index] = Issue{Path: issue.Path, Message: IssueMessage(definition, issue)}
	}
	return result
}

// IssueMessage uses the same schema titles as the form, keeping JSON paths out of
// user-facing validation errors. The structured Issue still retains its path.
func IssueMessage(definition json.RawMessage, issue Issue) string {
	var rule map[string]any
	_ = json.Unmarshal(definition, &rule)
	labels := []string{}
	for _, part := range issuePathPart.FindAllString(strings.TrimPrefix(issue.Path, "$"), -1) {
		if index, err := strconv.Atoi(part); err == nil {
			labels = append(labels, "Item "+strconv.Itoa(index+1))
			rule, _ = rule["items"].(map[string]any)
			continue
		}
		properties, _ := rule["properties"].(map[string]any)
		rule, _ = properties[part].(map[string]any)
		title, _ := rule["title"].(string)
		if title == "" {
			title = strings.ReplaceAll(fieldWordBoundary.ReplaceAllString(part, "$1 $2"), "_", " ")
			letters := []rune(title)
			if len(letters) > 0 {
				letters[0] = unicode.ToUpper(letters[0])
				title = string(letters)
			}
		}
		labels = append(labels, title)
	}
	if len(labels) == 0 {
		return issue.Message
	}
	return strings.Join(labels, " · ") + ": " + issue.Message
}
