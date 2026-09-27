package camoufoxnative

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"
)

// accessTokenPath is the RPC path for reading Drive authorization token from official site
const accessTokenPath = "/$rpc/google.internal.alkali.applications.makersuite.v1.MakerSuiteService/GenerateAccessToken"

// driveConsentTimeout is the maximum duration for a Drive authorization flow
const driveConsentTimeout = 3 * time.Minute

// driveAccessButtonExpression returns the unobscured Allow Drive access button on the History page
const driveAccessButtonExpression = `[...document.querySelectorAll('button[aria-label="Allow Drive access"]')].find(button => {
  const box = button.getBoundingClientRect();
  return button.contains(document.elementFromPoint(box.left + box.width / 2, box.top + box.height / 2));
}) || null`

// driveApproveExpression returns the Allow button on OAuth consent page
const driveApproveExpression = `document.querySelector('#submit_approve_access')`

// authorizeDrive confirms on the official site that the account has authorized Google Drive, completing OAuth consent if not authorized
func (session *loginSession) authorizeDrive(ctx context.Context, email string) error {
	ctx, cancel := context.WithTimeout(ctx, driveConsentTimeout)
	defer cancel()
	status, err := session.waitAccessTokenStatus(ctx)
	if err != nil {
		return err
	}
	if status == http.StatusOK {
		return nil
	}
	if status != http.StatusUnauthorized {
		return fmt.Errorf("GenerateAccessToken returned HTTP %d", status)
	}
	if err := session.navigate(ctx, aiStudioOrigin+"/library"); err != nil {
		return err
	}
	known, err := session.topLevelContexts(ctx)
	if err != nil {
		return err
	}
	popup := ""
	for popup == "" {
		if err := session.clickWhenPresent(ctx, session.contextID, driveAccessButtonExpression); err != nil {
			return fmt.Errorf("open Drive authorization: %w", err)
		}
		attemptCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		popup, err = session.waitPopup(attemptCtx, known)
		cancel()
		if err != nil && ctx.Err() != nil {
			return err
		}
	}
	if err := session.completeConsent(ctx, popup, email); err != nil {
		return err
	}
	session.client.accessTokenStatus = 0
	if err := session.navigate(ctx, aiStudioOrigin+"/prompts/new_chat"); err != nil {
		return err
	}
	status, err = session.waitAccessTokenStatus(ctx)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("GenerateAccessToken returned HTTP %d after Drive authorization", status)
	}
	return nil
}

// waitAccessTokenStatus waits for the official site page to complete one GenerateAccessToken
func (session *loginSession) waitAccessTokenStatus(ctx context.Context) (int, error) {
	for session.client.accessTokenStatus == 0 {
		if _, err := session.client.evaluate(ctx, session.contextID, "0"); err != nil && !retryablePageEvaluation(err) {
			return 0, fmt.Errorf("wait for GenerateAccessToken: %w", err)
		}
		if err := waitContext(ctx, 200*time.Millisecond); err != nil {
			return 0, fmt.Errorf("wait for GenerateAccessToken: %w", err)
		}
	}
	return session.client.accessTokenStatus, nil
}

// navigate opens official site page in the login tab
func (session *loginSession) navigate(ctx context.Context, pageURL string) error {
	if _, err := session.client.command(ctx, "browsingContext.navigate", map[string]any{
		"context": session.contextID,
		"url":     pageURL,
		"wait":    "interactive",
	}); err != nil && !strings.Contains(err.Error(), "NS_ERROR_ABORT") {
		return fmt.Errorf("navigate to %s: %w", pageURL, err)
	}
	return nil
}

// topLevelContexts returns all current top-level tabs and their URLs
func (session *loginSession) topLevelContexts(ctx context.Context) (map[string]string, error) {
	tree, err := session.client.command(ctx, "browsingContext.getTree", map[string]any{"maxDepth": 0})
	if err != nil {
		return nil, fmt.Errorf("read browser tabs: %w", err)
	}
	contexts := make(map[string]string)
	items, _ := tree["contexts"].([]any)
	for _, item := range items {
		value, _ := item.(map[string]any)
		id, _ := value["context"].(string)
		address, _ := value["url"].(string)
		if id != "" {
			contexts[id] = address
		}
	}
	return contexts, nil
}

// waitPopup waits for OAuth popup opened by official site
func (session *loginSession) waitPopup(ctx context.Context, known map[string]string) (string, error) {
	for {
		contexts, err := session.topLevelContexts(ctx)
		if err != nil {
			return "", err
		}
		for id := range contexts {
			if _, ok := known[id]; !ok {
				return id, nil
			}
		}
		if err := waitContext(ctx, 200*time.Millisecond); err != nil {
			return "", fmt.Errorf("wait for Drive authorization popup: %w", err)
		}
	}
}

// completeConsent selects account and approves consent in OAuth popup until the popup closes
func (session *loginSession) completeConsent(ctx context.Context, popup string, email string) error {
	accountExpression := consentAccountExpression(email)
	lastURL := ""
	for {
		contexts, err := session.topLevelContexts(ctx)
		if err != nil {
			return err
		}
		pageURL, open := contexts[popup]
		if !open {
			return nil
		}
		lastURL = pageURL
		step, err := session.client.evaluateString(ctx, popup, fmt.Sprintf(
			`(() => (%s) ? 'approve' : (%s) ? 'account' : '')()`, driveApproveExpression, accountExpression,
		))
		switch {
		case err != nil && !retryablePageEvaluation(err):
			return fmt.Errorf("read Drive authorization popup: %w", err)
		case step == "approve":
			err = session.click(ctx, popup, driveApproveExpression)
		case step == "account":
			err = session.click(ctx, popup, accountExpression)
		}
		if err != nil && !retryablePageEvaluation(err) && !errors.Is(err, errElementMissing) {
			return fmt.Errorf("operate Drive authorization popup: %w", err)
		}
		if err := waitContext(ctx, 500*time.Millisecond); err != nil {
			return fmt.Errorf("Drive authorization incomplete, popup stopped at %s: %w", lastURL, err)
		}
	}
}

// consentAccountExpression returns target account entry in account selector page
func consentAccountExpression(email string) string {
	encoded, _ := json.Marshal(strings.ToLower(strings.TrimSpace(email)))
	return fmt.Sprintf(`((email) => {
  const items = [...document.querySelectorAll('[data-email]')];
  return items.find(item => (item.getAttribute('data-email') || '').toLowerCase() === email) || (items.length === 1 ? items[0] : null);
})(%s)`, encoded)
}

// errElementMissing indicates the click target has not yet appeared on the page
var errElementMissing = errors.New("page element not found")

// clickWhenPresent waits for element to appear and clicks it
func (session *loginSession) clickWhenPresent(ctx context.Context, contextID string, expression string) error {
	for {
		err := session.click(ctx, contextID, expression)
		if err == nil {
			return nil
		}
		if !errors.Is(err, errElementMissing) && !retryablePageEvaluation(err) {
			return err
		}
		if err := waitContext(ctx, 300*time.Millisecond); err != nil {
			return err
		}
	}
}

// click clicks the center of the element returned by expression using real pointer events
func (session *loginSession) click(ctx context.Context, contextID string, expression string) error {
	encoded, err := session.client.evaluateString(ctx, contextID, fmt.Sprintf(`(() => {
  const element = %s;
  if (!element) return '';
  element.scrollIntoView({block: 'center', inline: 'center'});
  const box = element.getBoundingClientRect();
  return JSON.stringify([box.left + box.width / 2, box.top + box.height / 2]);
})()`, expression))
	if err != nil {
		return err
	}
	if encoded == "" {
		return errElementMissing
	}
	var point [2]float64
	if err := json.Unmarshal([]byte(encoded), &point); err != nil {
		return fmt.Errorf("parse element position: %w", err)
	}
	x, y := int(math.Round(point[0])), int(math.Round(point[1]))
	_, err = session.client.command(ctx, "input.performActions", map[string]any{
		"context": contextID,
		"actions": []map[string]any{{
			"type":       "pointer",
			"id":         "mouse",
			"parameters": map[string]any{"pointerType": "mouse"},
			"actions": []map[string]any{
				{"type": "pointerMove", "x": x, "y": y},
				{"type": "pointerDown", "button": 0},
				{"type": "pointerUp", "button": 0},
			},
		}},
	})
	return err
}
