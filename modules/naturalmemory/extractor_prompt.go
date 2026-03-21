package naturalmemory

import (
	"fmt"
	"strings"
	"time"

	"ex-otogi/pkg/otogi/ai"
)

const extractionSystemPrompt = `You are a memory extraction system. You analyze conversation text and extract facts worth remembering for future conversations.`

func renderExtractionPrompt(contextWindow extractionContext, existingMemories []ai.LLMMemoryRecord) string {
	var builder strings.Builder

	builder.WriteString("Analyze the conversation below and extract facts worth remembering for future conversations in this chat.\n\n")
	builder.WriteString("Extract ONLY:\n")
	builder.WriteString("- User preferences, habits, or personal facts stated by participants\n")
	builder.WriteString("- Important decisions, agreements, or plans\n")
	builder.WriteString("- Knowledge participants shared about themselves, their work, or their context\n")
	builder.WriteString("- Corrections to previously known information\n")
	builder.WriteString("- Notable experiences or events mentioned\n")
	builder.WriteString("- Time-bounded states (medications, travel plans, temporary situations) that have a clear expiration\n\n")
	builder.WriteString("Do NOT extract:\n")
	builder.WriteString("- Greetings, small talk, or ephemeral pleasantries\n")
	builder.WriteString("- General knowledge the assistant provided without participant-specific relevance\n")
	builder.WriteString("- Trivial or obvious information\n")
	builder.WriteString("- Information already present in the existing memories below\n\n")
	builder.WriteString("Normalization rules:\n")
	builder.WriteString("- Each memory must be a self-contained sentence that still makes sense out of context\n")
	builder.WriteString("- Resolve pronouns and nicknames to explicit participant names when possible\n")
	builder.WriteString("- Convert relative time references such as today, tomorrow, or next week into absolute dates using the anchor time\n")
	builder.WriteString("- Include subject_actor_id and subject_actor_name when the memory is primarily about one participant\n")
	builder.WriteString("- If a fact has a clear expiration date or is time-bounded, include valid_until as an ISO 8601 timestamp\n")
	builder.WriteString("- Include keywords (3-5 key terms that would help retrieve this memory) and tags (1-3 categorical labels)\n\n")
	builder.WriteString("<anchor_time>\n")
	builder.WriteString(contextWindow.AnchorTime.UTC().Format(time.RFC3339))
	builder.WriteString("\n</anchor_time>\n\n")
	builder.WriteString("<participants>\n")
	for _, participant := range contextWindow.Participants {
		builder.WriteString(fmt.Sprintf(
			"<participant id=%q is_bot=%q>%s</participant>\n",
			participant.ID,
			fmt.Sprintf("%t", participant.IsBot),
			participant.Name,
		))
	}
	builder.WriteString("</participants>\n\n")
	builder.WriteString("<existing_memories>\n")
	for _, memory := range existingMemories {
		builder.WriteString(fmt.Sprintf("<memory category=%q>%s</memory>\n", memory.Category, memory.Content))
	}
	builder.WriteString("</existing_memories>\n\n")
	builder.WriteString("<conversation>\n")
	builder.WriteString(strings.TrimSpace(contextWindow.ConversationText))
	builder.WriteString("\n</conversation>\n\n")
	builder.WriteString("Respond with a JSON array. Return [] if nothing new is worth remembering.\n")
	builder.WriteString(`Format: [{"content":"...","category":"user_fact|preference|knowledge|experience","importance":1,"subject_actor_id":"...","subject_actor_name":"...","valid_until":"","keywords":["term1","term2"],"tags":["label1"]}]`)

	return builder.String()
}
