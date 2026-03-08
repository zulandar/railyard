package bull

import (
	"testing"

	"github.com/google/go-github/v68/github"
)

func TestFilterIssue_NilIssue(t *testing.T) {
	result := FilterIssue(nil, "ignore", nil)
	if result.Pass {
		t.Fatal("expected nil issue to be rejected")
	}
	if result.Reason != "nil issue" {
		t.Fatalf("unexpected reason: %s", result.Reason)
	}
}

func TestFilterIssue_PullRequest(t *testing.T) {
	issue := &github.Issue{
		Number:           github.Ptr(1),
		Title:            github.Ptr("some PR"),
		Body:             github.Ptr("this is a pull request body that is long enough"),
		PullRequestLinks: &github.PullRequestLinks{URL: github.Ptr("https://example.com")},
	}
	result := FilterIssue(issue, "ignore", nil)
	if result.Pass {
		t.Fatal("expected pull request to be rejected")
	}
	if result.Reason != "pull request, not an issue" {
		t.Fatalf("unexpected reason: %s", result.Reason)
	}
}

func TestFilterIssue_IgnoreLabel(t *testing.T) {
	issue := &github.Issue{
		Number: github.Ptr(2),
		Title:  github.Ptr("some issue"),
		Body:   github.Ptr("this body is long enough for the filter"),
		Labels: []*github.Label{
			{Name: github.Ptr("bug")},
			{Name: github.Ptr("bull:ignore")},
		},
	}
	result := FilterIssue(issue, "bull:ignore", nil)
	if result.Pass {
		t.Fatal("expected issue with ignore label to be rejected")
	}
	if result.Reason != "has ignore label" {
		t.Fatalf("unexpected reason: %s", result.Reason)
	}
}

func TestFilterIssue_EmptyBody(t *testing.T) {
	issue := &github.Issue{
		Number: github.Ptr(3),
		Title:  github.Ptr("empty body issue"),
		Body:   github.Ptr(""),
	}
	result := FilterIssue(issue, "ignore", nil)
	if result.Pass {
		t.Fatal("expected empty body to be rejected")
	}
	if result.Reason != "body too short (minimum 20 characters)" {
		t.Fatalf("unexpected reason: %s", result.Reason)
	}
}

func TestFilterIssue_ShortBody(t *testing.T) {
	issue := &github.Issue{
		Number: github.Ptr(4),
		Title:  github.Ptr("short body issue"),
		Body:   github.Ptr("too short"),
	}
	result := FilterIssue(issue, "ignore", nil)
	if result.Pass {
		t.Fatal("expected short body to be rejected")
	}
	if result.Reason != "body too short (minimum 20 characters)" {
		t.Fatalf("unexpected reason: %s", result.Reason)
	}
}

func TestFilterIssue_NilBody(t *testing.T) {
	issue := &github.Issue{
		Number: github.Ptr(5),
		Title:  github.Ptr("nil body issue"),
	}
	result := FilterIssue(issue, "ignore", nil)
	if result.Pass {
		t.Fatal("expected nil body to be rejected")
	}
	if result.Reason != "body too short (minimum 20 characters)" {
		t.Fatalf("unexpected reason: %s", result.Reason)
	}
}

func TestFilterIssue_AlreadyTracked(t *testing.T) {
	issue := &github.Issue{
		Number: github.Ptr(10),
		Title:  github.Ptr("a tracked issue title"),
		Body:   github.Ptr("this body is definitely long enough"),
	}
	tracked := []ExistingIssue{
		{IssueNumber: 10, Title: "a tracked issue title"},
		{IssueNumber: 20, Title: "another issue"},
	}
	result := FilterIssue(issue, "ignore", tracked)
	if result.Pass {
		t.Fatal("expected already tracked issue to be rejected")
	}
	if result.Reason != "already tracked as bull issue" {
		t.Fatalf("unexpected reason: %s", result.Reason)
	}
}

func TestFilterIssue_DuplicateTitleExactMatch(t *testing.T) {
	issue := &github.Issue{
		Number: github.Ptr(11),
		Title:  github.Ptr("Database connection timeout"),
		Body:   github.Ptr("this body is definitely long enough"),
	}
	tracked := []ExistingIssue{
		{IssueNumber: 5, Title: "database connection timeout"},
	}
	result := FilterIssue(issue, "ignore", tracked)
	if result.Pass {
		t.Fatal("expected duplicate title to be rejected")
	}
	if result.Reason != "duplicate of existing issue #5" {
		t.Fatalf("unexpected reason: %s", result.Reason)
	}
}

func TestFilterIssue_DuplicateTitleContains(t *testing.T) {
	issue := &github.Issue{
		Number: github.Ptr(12),
		Title:  github.Ptr("Database connection timeout in production"),
		Body:   github.Ptr("this body is definitely long enough"),
	}
	tracked := []ExistingIssue{
		{IssueNumber: 5, Title: "database connection timeout"},
	}
	result := FilterIssue(issue, "ignore", tracked)
	if result.Pass {
		t.Fatal("expected duplicate title (contains) to be rejected")
	}
	if result.Reason != "duplicate of existing issue #5" {
		t.Fatalf("unexpected reason: %s", result.Reason)
	}
}

func TestFilterIssue_ValidIssue(t *testing.T) {
	issue := &github.Issue{
		Number: github.Ptr(99),
		Title:  github.Ptr("Add support for PostgreSQL 16"),
		Body:   github.Ptr("We need to add support for PostgreSQL 16 features including..."),
		Labels: []*github.Label{
			{Name: github.Ptr("enhancement")},
		},
	}
	tracked := []ExistingIssue{
		{IssueNumber: 1, Title: "Fix login bug"},
		{IssueNumber: 2, Title: "Update docs"},
	}
	result := FilterIssue(issue, "ignore", tracked)
	if !result.Pass {
		t.Fatalf("expected valid issue to pass, got rejected with reason: %s", result.Reason)
	}
}

func TestFilterIssue_EmptyTrackedTitleNotDuplicate(t *testing.T) {
	issue := &github.Issue{
		Number: github.Ptr(13),
		Title:  github.Ptr("Brand new issue title"),
		Body:   github.Ptr("this body is definitely long enough"),
	}
	tracked := []ExistingIssue{
		{IssueNumber: 5, Title: ""},
	}
	result := FilterIssue(issue, "ignore", tracked)
	if !result.Pass {
		t.Fatalf("expected issue with empty tracked title to pass, got rejected with reason: %s", result.Reason)
	}
}

func TestFilterIssue_Exactly20CharBody(t *testing.T) {
	issue := &github.Issue{
		Number: github.Ptr(100),
		Title:  github.Ptr("edge case body length"),
		Body:   github.Ptr("12345678901234567890"), // exactly 20 chars
	}
	result := FilterIssue(issue, "ignore", nil)
	if !result.Pass {
		t.Fatalf("expected 20-char body to pass, got rejected with reason: %s", result.Reason)
	}
}
