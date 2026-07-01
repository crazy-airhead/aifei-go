package xxljob

// Generic response.
type res struct {
	Code int64       `json:"code"` // 200 for success, others for failure
	Msg  interface{} `json:"msg"`  // error message
}

/*****************  Upstream parameters  *********************/

// Registry registration params.
type Registry struct {
	RegistryGroup string `json:"registryGroup"`
	RegistryKey   string `json:"registryKey"`
	RegistryValue string `json:"registryValue"`
}

// Callback payload after task completion.
type call []*callElement

type callElement struct {
	LogID         int64          `json:"logId"`
	LogDateTim    int64          `json:"logDateTim"`
	ExecuteResult *ExecuteResult `json:"executeResult"`
	// Fields used by xxl-job v2.3.0+
	HandleCode int    `json:"handleCode"` // 200=success, 500=failure
	HandleMsg  string `json:"handleMsg"`
}

// ExecuteResult task execution result: 200=success, 500=failure.
type ExecuteResult struct {
	Code int64       `json:"code"`
	Msg  interface{} `json:"msg"`
}

/*****************  Downstream parameters  *********************/

// Blocking strategies.
const (
	serialExecution = "SERIAL_EXECUTION" // single-machine serial
	discardLater    = "DISCARD_LATER"    // discard subsequent
	coverEarly      = "COVER_EARLY"      // cover previous
)

// RunReq task trigger request.
type RunReq struct {
	JobID                 int64  `json:"jobId"`                 // task ID
	ExecutorHandler       string `json:"executorHandler"`       // task handler name
	ExecutorParams        string `json:"executorParams"`        // task params
	ExecutorBlockStrategy string `json:"executorBlockStrategy"` // blocking strategy
	ExecutorTimeout       int64  `json:"executorTimeout"`       // timeout in seconds, effective when > 0
	LogID                 int64  `json:"logId"`                 // schedule log ID
	LogDateTime           int64  `json:"logDateTime"`           // schedule log time
	GlueType              string `json:"glueType"`              // task mode, see GlueTypeEnum
	GlueSource            string `json:"glueSource"`            // GLUE script
	GlueUpdatetime        int64  `json:"glueUpdatetime"`        // GLUE script update time
	BroadcastIndex        int64  `json:"broadcastIndex"`        // shard index
	BroadcastTotal        int64  `json:"broadcastTotal"`        // total shards
}

// killReq kill task request.
type killReq struct {
	JobID int64 `json:"jobId"` // task ID
}

// idleBeatReq idle beat request.
type idleBeatReq struct {
	JobID int64 `json:"jobId"` // task ID
}

// LogReq log query request.
type LogReq struct {
	LogDateTim  int64 `json:"logDateTim"`  // schedule log time
	LogID       int64 `json:"logId"`       // schedule log ID
	FromLineNum int   `json:"fromLineNum"` // starting line number, for scroll loading
}

// LogRes log query response.
type LogRes struct {
	Code    int64         `json:"code"`    // 200=success, others=failure
	Msg     string        `json:"msg"`     // error message
	Content LogResContent `json:"content"` // log content
}

// LogResContent log response content.
type LogResContent struct {
	FromLineNum int    `json:"fromLineNum"` // starting line number
	ToLineNum   int    `json:"toLineNum"`   // ending line number
	LogContent  string `json:"logContent"`  // log content
	IsEnd       bool   `json:"isEnd"`       // whether all logs are loaded
}
