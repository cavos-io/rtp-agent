package workflows

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/core/llm"
)

type GetAddressResult struct {
	Address string
}

type GetAddressOptions struct {
	RequireConfirmation    bool
	RequireConfirmationSet bool
}

type GetAddressTask struct {
	agent.AgentTask[*GetAddressResult]
	RequireConfirmation bool
	currentAddress      string
	addressConfirmed    bool
}

const addressConfirmationInstruction = "Call `confirm_address` after the user confirmed the address is correct."

const addressInstructionsBeforeConfirmation = `You are only a single step in a broader system, responsible solely for capturing an address.
You will be handling addresses from any country. Expect that users will say address in different formats with fields filled like:
- 'street_address': '450 SOUTH MAIN ST', 'unit_number': 'FLOOR 2', 'locality': 'SALT LAKE CITY UT 84101', 'country': 'UNITED STATES',
- 'street_address': '123 MAPLE STREET', 'unit_number': 'APARTMENT 10', 'locality': 'OTTAWA ON K1A 0B1', 'country': 'CANADA',
- 'street_address': 'GUOMAO JIE 3 HAO, CHAOYANG QU', 'unit_number': 'GUOMAO DA SHA 18 LOU 101 SHI', 'locality': 'BEIJING SHI 100000', 'country': 'CHINA',
- 'street_address': '5 RUE DE L'ANCIENNE COMÉDIE', 'unit_number': 'APP C4', 'locality': '75006 PARIS', 'country': 'FRANCE',
- 'street_address': 'PLOT 10, NEHRU ROAD', 'unit_number': 'OFFICE 403, 4TH FLOOR', 'locality': 'VILE PARLE (E), MUMBAI MAHARASHTRA 400099', 'country': 'INDIA',
Normalize common spoken patterns silently:
- Convert words like 'dash' and 'apostrophe' into symbols: -, '.
- Convert spelled out numbers like 'six' and 'seven' into numerals: 6, 7.
- Recognize patterns where users speak their address field followed by spelling: e.g., 'guomao g u o m a o'.
- Filter out filler words or hesitations.
- Recognize when there may be accents on certain letters if explicitly said or common in the location specified. Be sure to verify the correct accents if existent.
Don't mention corrections. Treat inputs as possibly imperfect but fix them silently.
Call ` + "`update_address`" + ` at the first opportunity whenever you form a new hypothesis about the address. (before asking any questions or providing any answers.)
Don't invent new addresses, stick strictly to what the user said.
`

const addressInstructionsAfterConfirmation = `When reading a numerical ordinal suffix (st, nd, rd, th), the number must be verbally expanded into its full, correctly pronounced word form.
Do not read the number and the suffix letters separately.
Confirm postal codes by reading them out digit-by-digit as a sequence of single numbers. Do not read them as cardinal numbers.
For example, read 90210 as 'nine zero two one zero.'
Avoid using bullet points and parenthese in any responses.
Spell out the address letter-by-letter when applicable, such as street names and provinces, especially when the user spells it out initially.
If the address is unclear or invalid, or it takes too much back-and-forth, prompt for it in parts in this order: street address, unit number if applicable, locality, and country.
Ignore unrelated input and avoid going off-topic. Do not generate markdown, greetings, or unnecessary commentary.
Always explicitly invoke a tool when applicable. Do not simulate tool usage, no real action is taken unless the tool is explicitly called.`

const AddressInstructions = addressInstructionsBeforeConfirmation + addressConfirmationInstruction + "\n" + addressInstructionsAfterConfirmation

const addressInstructionsWithoutConfirmation = addressInstructionsBeforeConfirmation + addressInstructionsAfterConfirmation

func NewGetAddressTask(opts GetAddressOptions) *GetAddressTask {
	requireConfirmation := true
	if opts.RequireConfirmationSet {
		requireConfirmation = opts.RequireConfirmation
	}
	instructions := AddressInstructions
	if !requireConfirmation {
		instructions = addressInstructionsWithoutConfirmation
	}
	t := &GetAddressTask{
		AgentTask:           *agent.NewAgentTask[*GetAddressResult](instructions),
		RequireConfirmation: requireConfirmation,
	}

	t.Agent.Tools = []llm.Tool{
		&updateAddressTool{task: t},
		&declineAddressCaptureTool{task: t},
	}

	return t
}

func (t *GetAddressTask) OnEnter() {
	if activity := t.Agent.GetActivity(); activity != nil {
		if session := activity.Session; session != nil {
			_, _ = session.GenerateReply(context.Background(), addressOnEnterPrompt())
		}
	}
}

func addressOnEnterPrompt() string {
	return "Ask the user to provide their address."
}

type updateAddressTool struct {
	task *GetAddressTask
}

func (t *updateAddressTool) ID() string   { return "update_address" }
func (t *updateAddressTool) Name() string { return "update_address" }
func (t *updateAddressTool) Description() string {
	return "Update the address provided by the user."
}
func (t *updateAddressTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"street_address": map[string]any{
				"type":        "string",
				"description": "Dependent on country, may include fields like house number, street name, block, or district",
			},
			"unit_number": map[string]any{
				"type":        "string",
				"description": "The unit number, for example Floor 1 or Apartment 12. If there is no unit number, return ''",
			},
			"locality": map[string]any{
				"type":        "string",
				"description": "Dependent on country, may include fields like city, zip code, or province",
			},
			"country": map[string]any{
				"type":        "string",
				"description": "The country the user lives in spelled out fully",
			},
		},
		"required": []string{"street_address", "unit_number", "locality", "country"},
	}
}

func (t *updateAddressTool) Execute(ctx context.Context, args string) (string, error) {
	var params struct {
		StreetAddress string `json:"street_address"`
		UnitNumber    string `json:"unit_number"`
		Locality      string `json:"locality"`
		Country       string `json:"country"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", err
	}

	addressFields := []string{params.StreetAddress}
	if strings.TrimSpace(params.UnitNumber) != "" {
		addressFields = append(addressFields, params.UnitNumber)
	}
	addressFields = append(addressFields, params.Locality, params.Country)
	address := strings.Join(addressFields, " ")

	t.task.currentAddress = address

	if !t.task.RequireConfirmation {
		_ = t.task.Complete(&GetAddressResult{Address: address})
		return "Address captured and task completed.", nil
	}

	t.task.setConfirmAddressTool(address)
	return fmt.Sprintf("The address has been updated to %s\nRepeat the address field by field: %q if needed\nPrompt the user for confirmation, do not call `confirm_address` directly", address, addressFields), nil
}

func (t *GetAddressTask) setConfirmAddressTool(address string) {
	tools := make([]llm.Tool, 0, len(t.Agent.Tools)+1)
	for _, tool := range t.Agent.Tools {
		if tool.ID() == "confirm_address" {
			continue
		}
		tools = append(tools, tool)
	}
	tools = append(tools, &confirmAddressTool{task: t, address: address})
	t.Agent.Tools = tools
}

type confirmAddressTool struct {
	task    *GetAddressTask
	address string
}

func (t *confirmAddressTool) ID() string   { return "confirm_address" }
func (t *confirmAddressTool) Name() string { return "confirm_address" }
func (t *confirmAddressTool) Description() string {
	return "Call this tool when the user confirms that the address is correct."
}
func (t *confirmAddressTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

func (t *confirmAddressTool) Execute(ctx context.Context, args string) (string, error) {
	if t.task.currentAddress == "" {
		return "", fmt.Errorf("error: no address was provided, update_address must be called before")
	}
	if t.address != t.task.currentAddress {
		return "", llm.NewToolError("The address has changed since confirmation was requested, ask the user to confirm the updated address.")
	}

	t.task.addressConfirmed = true
	_ = t.task.Complete(&GetAddressResult{Address: t.address})
	return "Address confirmed.", nil
}

type declineAddressCaptureTool struct {
	task *GetAddressTask
}

func (t *declineAddressCaptureTool) ID() string   { return "decline_address_capture" }
func (t *declineAddressCaptureTool) Name() string { return "decline_address_capture" }
func (t *declineAddressCaptureTool) Description() string {
	return "Handles the case when the user explicitly declines to provide an address."
}
func (t *declineAddressCaptureTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"reason": map[string]any{"type": "string", "description": "A short explanation of why the user declined"},
		},
		"required": []string{"reason"},
	}
}

func (t *declineAddressCaptureTool) Execute(ctx context.Context, args string) (string, error) {
	var params struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", err
	}

	_ = t.task.Fail(fmt.Errorf("couldn't get the address: %s", params.Reason))
	return "Task failed.", nil
}
