package agui

// ==================== 状态/快照（STATE_* / MESSAGES_SNAPSHOT） ====================

// State 状态快照载荷（AG-UI 协议 State 对象；键值任意）。
type State struct {
	StateMap map[string]any `json:"state"`
}

func NewState(m map[string]any) *State { return &State{StateMap: m} }

// StateSnapshotEvent 状态快照。
type StateSnapshotEvent struct {
	Base
	State *State `json:"state"`
}

func NewStateSnapshotEvent(state *State) *StateSnapshotEvent {
	return &StateSnapshotEvent{Base{Type: EventStateSnapshot, Timestamp: nowMillis()}, state}
}

// MessagesSnapshotEvent 消息快照。
type MessagesSnapshotEvent struct {
	Base
	Messages []*Message `json:"messages"`
}

func NewMessagesSnapshotEvent(messages []*Message) *MessagesSnapshotEvent {
	return &MessagesSnapshotEvent{Base{Type: EventMessagesSnapshot, Timestamp: nowMillis()}, messages}
}

// RawEvent 原始供应商帧透传（rawEvent 为载荷本体）。
type RawEvent struct{ Base }

func NewRawEvent(raw any) *RawEvent {
	return &RawEvent{Base{Type: EventRaw, Timestamp: nowMillis(), RawEvent: raw}}
}

// CustomEvent 自定义事件（name + value；wrapper 把未建模 ChatEvent 归一到此）。
type CustomEvent struct {
	Base
	Name  string `json:"name"`
	Value any    `json:"value,omitempty"`
}

func NewCustomEvent(name string, value any) *CustomEvent {
	return &CustomEvent{Base{Type: EventCustom, Timestamp: nowMillis(), RawEvent: value}, name, value}
}

func (e *StateSnapshotEvent) eventType() EventType   { return e.Type }
func (e *StateDeltaEvent) eventType() EventType      { return e.Type }
func (e *MessagesSnapshotEvent) eventType() EventType { return e.Type }
func (e *RawEvent) eventType() EventType             { return e.Type }
func (e *CustomEvent) eventType() EventType          { return e.Type }
