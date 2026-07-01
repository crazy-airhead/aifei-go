package xxljob

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/crazy-airhead/aifei-go/log"
)

// Executor is the xxl-job executor interface.
type Executor interface {
	// Init initializes the executor.
	Init(...Option)
	// LogHandler sets the log query handler.
	LogHandler(handler LogHandler)
	// Use adds middleware.
	Use(middlewares ...Middleware)
	// RegTask registers a task handler.
	RegTask(pattern string, task TaskFunc)
	// RunTask handles a task run request.
	RunTask(writer http.ResponseWriter, request *http.Request)
	// KillTask handles a task kill request.
	KillTask(writer http.ResponseWriter, request *http.Request)
	// TaskLog handles a task log query.
	TaskLog(writer http.ResponseWriter, request *http.Request)
	// Beat handles a heartbeat check.
	Beat(writer http.ResponseWriter, request *http.Request)
	// IdleBeat handles an idle check.
	IdleBeat(writer http.ResponseWriter, request *http.Request)
	// Run starts the executor HTTP server in background and returns immediately.
	Run() error
	// Stop deregisters from the scheduling center and shuts down the HTTP server.
	Stop()
}

// NewExecutor creates a new executor with the given options.
func NewExecutor(opts ...Option) Executor {
	return newExecutor(opts...)
}

func newExecutor(opts ...Option) *executor {
	options := newOptions(opts...)
	e := &executor{
		opts: options,
	}
	return e
}

type executor struct {
	opts    Options
	address string
	regList *taskList // registered task list
	runList *taskList // running task list
	mu      sync.RWMutex
	log     log.Logger

	logHandler  LogHandler   // log query handler
	middlewares []Middleware // middleware chain
	server      *http.Server // HTTP server for graceful shutdown
}

func (e *executor) Init(opts ...Option) {
	for _, o := range opts {
		o(&e.opts)
	}
	e.log = e.opts.logger
	e.regList = &taskList{
		data: make(map[string]*Task),
	}
	e.runList = &taskList{
		data: make(map[string]*Task),
	}
	e.address = e.opts.ExecutorIp + ":" + e.opts.ExecutorPort
	go e.registry()
}

// LogHandler sets the log query handler.
func (e *executor) LogHandler(handler LogHandler) {
	e.logHandler = handler
}

// Use adds middleware to the executor.
func (e *executor) Use(middlewares ...Middleware) {
	e.middlewares = middlewares
}

// Run starts the executor HTTP server in a background goroutine and returns
// immediately. Use Stop() to gracefully shut down the server.
func (e *executor) Run() error {
	// Create router
	mux := http.NewServeMux()
	mux.HandleFunc("/run", e.runTask)
	mux.HandleFunc("/kill", e.killTask)
	mux.HandleFunc("/log", e.taskLog)
	mux.HandleFunc("/beat", e.beat)
	mux.HandleFunc("/idleBeat", e.idleBeat)

	// Create server
	e.server = &http.Server{
		Addr:         ":" + e.opts.ExecutorPort,
		WriteTimeout: time.Second * 3,
		Handler:      mux,
	}

	e.log.Info("Starting xxl-job executor at %s", e.address)

	// Start server in a goroutine
	go func() {
		if err := e.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			e.log.Error("xxl-job executor server error: %s", err.Error())
		}
	}()

	return nil
}

// Stop deregisters from the scheduling center and shuts down the HTTP server.
func (e *executor) Stop() {
	e.registryRemove()
	if e.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := e.server.Shutdown(ctx); err != nil {
			e.log.Error("xxl-job executor shutdown error: %s", err.Error())
		}
	}
}

// RegTask registers a task handler.
func (e *executor) RegTask(pattern string, task TaskFunc) {
	var t = &Task{}
	t.fn = e.chain(task)
	e.regList.Set(pattern, t)
}

// runTask handles a task run request.
func (e *executor) runTask(writer http.ResponseWriter, request *http.Request) {
	e.mu.Lock()
	defer e.mu.Unlock()

	req, _ := io.ReadAll(request.Body)
	param := &RunReq{}
	err := json.Unmarshal(req, &param)
	if err != nil {
		_, _ = writer.Write(returnCall(param, FailureCode, "params err"))
		e.log.Error("参数解析错误:%s", string(req))
		return
	}
	e.log.Info("任务参数:%v", param)
	if !e.regList.Exists(param.ExecutorHandler) {
		_, _ = writer.Write(returnCall(param, FailureCode, "Task not registered"))
		e.log.Error("任务[%d]没有注册:%s", param.JobID, param.ExecutorHandler)
		return
	}

	// Blocking strategy handling
	if e.runList.Exists(Int64ToStr(param.JobID)) {
		if param.ExecutorBlockStrategy == coverEarly { // cover previous
			oldTask := e.runList.Get(Int64ToStr(param.JobID))
			if oldTask != nil {
				oldTask.Cancel()
				e.runList.Del(Int64ToStr(oldTask.Id))
			}
		} else { // SERIAL_EXECUTION or DISCARD_LATER: reject
			_, _ = writer.Write(returnCall(param, FailureCode, "There are tasks running"))
			e.log.Error("任务[%d]已经在运行了:%s", param.JobID, param.ExecutorHandler)
			return
		}
	}

	cxt := context.Background()
	task := e.regList.Get(param.ExecutorHandler)
	if param.ExecutorTimeout > 0 {
		task.Ext, task.Cancel = context.WithTimeout(cxt, time.Duration(param.ExecutorTimeout)*time.Second)
	} else {
		task.Ext, task.Cancel = context.WithCancel(cxt)
	}
	task.Id = param.JobID
	task.Name = param.ExecutorHandler
	task.Param = param
	task.log = e.log

	e.runList.Set(Int64ToStr(task.Id), task)
	go task.Run(func(code int64, msg string) {
		e.callback(task, code, msg)
	})
	e.log.Info("任务[%d]开始执行:%s", param.JobID, param.ExecutorHandler)
	_, _ = writer.Write(returnGeneral())
}

// killTask handles a task kill request.
func (e *executor) killTask(writer http.ResponseWriter, request *http.Request) {
	e.mu.Lock()
	defer e.mu.Unlock()
	req, _ := io.ReadAll(request.Body)
	param := &killReq{}
	_ = json.Unmarshal(req, &param)
	if !e.runList.Exists(Int64ToStr(param.JobID)) {
		_, _ = writer.Write(returnKill(param, FailureCode))
		e.log.Error("任务[%d]没有运行", param.JobID)
		return
	}
	task := e.runList.Get(Int64ToStr(param.JobID))
	task.Cancel()
	e.runList.Del(Int64ToStr(param.JobID))
	_, _ = writer.Write(returnGeneral())
}

// taskLog handles a task log query.
func (e *executor) taskLog(writer http.ResponseWriter, request *http.Request) {
	var res *LogRes
	data, err := io.ReadAll(request.Body)
	req := &LogReq{}
	if err != nil {
		e.log.Error("日志请求失败:%s", err.Error())
		reqErrLogHandler(writer, req, err)
		return
	}
	err = json.Unmarshal(data, &req)
	if err != nil {
		e.log.Error("日志请求解析失败:%s", err.Error())
		reqErrLogHandler(writer, req, err)
		return
	}
	e.log.Info("日志请求参数:%+v", req)
	if e.logHandler != nil {
		res = e.logHandler(req)
	} else {
		res = defaultLogHandler(req)
	}
	str, _ := json.Marshal(res)
	_, _ = writer.Write(str)
}

// beat handles a heartbeat check.
func (e *executor) beat(writer http.ResponseWriter, request *http.Request) {
	e.log.Info("心跳检测")
	_, _ = writer.Write(returnGeneral())
}

// idleBeat handles an idle check.
func (e *executor) idleBeat(writer http.ResponseWriter, request *http.Request) {
	e.mu.Lock()
	defer e.mu.Unlock()
	defer request.Body.Close()
	req, _ := io.ReadAll(request.Body)
	param := &idleBeatReq{}
	err := json.Unmarshal(req, &param)
	if err != nil {
		_, _ = writer.Write(returnIdleBeat(FailureCode))
		e.log.Error("参数解析错误:%s", string(req))
		return
	}
	if e.runList.Exists(Int64ToStr(param.JobID)) {
		_, _ = writer.Write(returnIdleBeat(FailureCode))
		e.log.Error("idleBeat任务[%d]正在运行", param.JobID)
		return
	}
	e.log.Info("忙碌检测任务参数:%v", param)
	_, _ = writer.Write(returnGeneral())
}

// registry registers the executor with the scheduling center.
func (e *executor) registry() {
	t := time.NewTimer(time.Second * 0) // execute immediately
	defer t.Stop()
	req := &Registry{
		RegistryGroup: "EXECUTOR",
		RegistryKey:   e.opts.RegistryKey,
		RegistryValue: "http://" + e.address,
	}
	param, err := json.Marshal(req)
	if err != nil {
		e.log.Error("执行器注册信息解析失败:%s", err.Error())
		return
	}
	for {
		<-t.C
		t.Reset(time.Second * time.Duration(20)) // 20s heartbeat
		func() {
			result, err := e.post("/api/registry", string(param))
			if err != nil {
				e.log.Error("执行器注册失败1:%s", err.Error())
				return
			}
			defer result.Body.Close()
			body, err := io.ReadAll(result.Body)
			if err != nil {
				e.log.Error("执行器注册失败2:%s", err.Error())
				return
			}
			r := &res{}
			_ = json.Unmarshal(body, &r)
			if r.Code != SuccessCode {
				e.log.Error("执行器注册失败3:%s", string(body))
				return
			}
			e.log.Info("执行器注册成功:%s", string(body))
		}()
	}
}

// registryRemove deregisters the executor from the scheduling center.
func (e *executor) registryRemove() {
	t := time.NewTimer(time.Second * 0) // execute immediately
	defer t.Stop()
	req := &Registry{
		RegistryGroup: "EXECUTOR",
		RegistryKey:   e.opts.RegistryKey,
		RegistryValue: "http://" + e.address,
	}
	param, err := json.Marshal(req)
	if err != nil {
		e.log.Error("执行器摘除失败:%s", err.Error())
		return
	}
	res, err := e.post("/api/registryRemove", string(param))
	if err != nil {
		e.log.Error("执行器摘除失败:%s", err.Error())
		return
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	e.log.Info("执行器摘除成功:%s", string(body))
}

// callback sends the task result back to the scheduling center.
func (e *executor) callback(task *Task, code int64, msg string) {
	e.runList.Del(Int64ToStr(task.Id))
	res, err := e.post("/api/callback", string(returnCall(task.Param, code, msg)))
	if err != nil {
		e.log.Error("callback err: %s", err.Error())
		return
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		e.log.Error("callback ReadAll err: %s", err.Error())
		return
	}
	e.log.Info("任务回调成功:%s", string(body))
}

// post sends a POST request to the scheduling center.
func (e *executor) post(action, body string) (resp *http.Response, err error) {
	request, err := http.NewRequest("POST", e.opts.ServerAddr+action, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json;charset=UTF-8")
	request.Header.Set("XXL-JOB-ACCESS-TOKEN", e.opts.AccessToken)
	client := http.Client{
		Timeout: e.opts.Timeout,
	}
	return client.Do(request)
}

// RunTask is the public handler for task run requests.
func (e *executor) RunTask(writer http.ResponseWriter, request *http.Request) {
	e.runTask(writer, request)
}

// KillTask is the public handler for task kill requests.
func (e *executor) KillTask(writer http.ResponseWriter, request *http.Request) {
	e.killTask(writer, request)
}

// TaskLog is the public handler for task log queries.
func (e *executor) TaskLog(writer http.ResponseWriter, request *http.Request) {
	e.taskLog(writer, request)
}

// Beat is the public handler for heartbeat checks.
func (e *executor) Beat(writer http.ResponseWriter, request *http.Request) {
	e.beat(writer, request)
}

// IdleBeat is the public handler for idle checks.
func (e *executor) IdleBeat(writer http.ResponseWriter, request *http.Request) {
	e.idleBeat(writer, request)
}
