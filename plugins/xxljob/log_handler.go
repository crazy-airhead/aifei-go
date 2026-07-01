package xxljob

import (
	"encoding/json"
	"net/http"
)

// LogHandler is used to query logs and display them in the xxl-job-admin dashboard.
type LogHandler func(req *LogReq) *LogRes

// defaultLogHandler returns a default log response.
func defaultLogHandler(req *LogReq) *LogRes {
	return &LogRes{Code: SuccessCode, Msg: "", Content: LogResContent{
		FromLineNum: req.FromLineNum,
		ToLineNum:   2,
		LogContent:  "This is the default log response, indicating that no LogHandler has been set.",
		IsEnd:       true,
	}}
}

// reqErrLogHandler writes a log error response.
func reqErrLogHandler(w http.ResponseWriter, req *LogReq, err error) {
	res := &LogRes{Code: FailureCode, Msg: err.Error(), Content: LogResContent{
		FromLineNum: req.FromLineNum,
		ToLineNum:   0,
		LogContent:  err.Error(),
		IsEnd:       true,
	}}
	str, _ := json.Marshal(res)
	_, _ = w.Write(str)
}
