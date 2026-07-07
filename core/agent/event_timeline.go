package agent

const allAgentEventsType = "*"

type AgentEvent = Event

type EventTimeline struct {
	session *AgentSession
}

func NewEventTimeline(session *AgentSession) *EventTimeline {
	return &EventTimeline{session: session}
}

func (t *EventTimeline) AddSubscriber(callback func(Event)) func() {
	if t == nil || t.session == nil || callback == nil {
		return func() {}
	}
	return t.session.On(allAgentEventsType, callback)
}

func (t *EventTimeline) AddEvent(ev Event) {
	if t == nil || t.session == nil || ev == nil {
		return
	}
	t.session.EmitEvent(ev)
}
