package xxljob

import (
	"encoding/json"
	"strconv"
)

// Int64ToStr converts int64 to string.
func Int64ToStr(i int64) string {
	return strconv.FormatInt(i, 10)
}

// returnCall builds the callback JSON payload.
func returnCall(req *RunReq, code int64, msg string) []byte {
	data := call{
		&callElement{
			LogID:      req.LogID,
			LogDateTim: req.LogDateTime,
			ExecuteResult: &ExecuteResult{
				Code: code,
				Msg:  msg,
			},
			HandleCode: int(code),
			HandleMsg:  msg,
		},
	}
	str, _ := json.Marshal(data)
	return str
}

// returnKill builds the kill response.
func returnKill(req *killReq, code int64) []byte {
	msg := ""
	if code != SuccessCode {
		msg = "Task kill err"
	}
	data := res{
		Code: code,
		Msg:  msg,
	}
	str, _ := json.Marshal(data)
	return str
}

// returnIdleBeat builds the idle-beat response.
func returnIdleBeat(code int64) []byte {
	msg := ""
	if code != SuccessCode {
		msg = "Task is busy"
	}
	data := res{
		Code: code,
		Msg:  msg,
	}
	str, _ := json.Marshal(data)
	return str
}

// returnGeneral builds a generic success response.
func returnGeneral() []byte {
	data := &res{
		Code: SuccessCode,
		Msg:  "",
	}
	str, _ := json.Marshal(data)
	return str
}
