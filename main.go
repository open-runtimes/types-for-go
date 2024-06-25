package types

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"os"
	"time"
)

const LOGGER_TYPE_LOG = "log"
const LOGGER_TYPE_ERROR = "error"

type Context struct {
	_Logger Logger
	Req     Request
	Res     Response
}

type Log struct {
	Message string
}

func (l Log) String() string {
	return l.Message
}

func (c *Context) Log(message interface{}) {
	c._Logger.Write(message, LOGGER_TYPE_LOG, false)
}

func (c *Context) Error(message interface{}) {
	c._Logger.Write(message, LOGGER_TYPE_LOG, false)
}

type Request struct {
	BodyRaw     string
	Body        interface{}
	Headers     map[string]string
	Method      string
	Url         string
	Path        string
	Port        int
	Scheme      string
	Host        string
	QueryString string
	Query       map[string]string
}

type ResponseOutput struct {
	Body       string
	StatusCode int
	Headers    map[string]string
}

type Response struct{}

func (r Response) Send(body string, statusCode int, headers map[string]string) ResponseOutput {
	if headers == nil {
		headers = map[string]string{}
	}

	if statusCode == 0 {
		statusCode = 200
	}

	return ResponseOutput{
		Body:       body,
		StatusCode: statusCode,
		Headers:    headers,
	}
}

func (r Response) Json(bodyStruct interface{}, statusCode int, headers map[string]string) ResponseOutput {
	if headers == nil {
		headers = map[string]string{}
	}

	if statusCode == 0 {
		statusCode = 200
	}

	headers["content-type"] = "application/json"

	jsonData, err := json.Marshal(bodyStruct)
	if err != nil {
		return r.Send("Error encoding JSON.", 500, nil)
	}

	jsonString := string(jsonData[:])

	return r.Send(jsonString, statusCode, headers)
}

func (r Response) Empty() ResponseOutput {
	return r.Send("", 204, map[string]string{})
}

func (r Response) Redirect(url string, statusCode int, headers map[string]string) ResponseOutput {
	if headers == nil {
		headers = map[string]string{}
	}

	if statusCode == 0 {
		statusCode = 301
	}

	headers["location"] = url

	return r.Send("", statusCode, headers)
}

type Logger struct {
	Enabled            bool
	Id                 string
	IncludesNativeInfo bool

	StreamLogs   *os.File
	StreamErrors *os.File

	NativeStreamLogs   chan string
	NativeStreamErrors chan string

	WriterLogs   *os.File
	WriterErrors *os.File

	NativeLogsCache   *os.File
	NativeErrorsCache *os.File
}

func NewLogger(status string, id string) (Logger, error) {
	logger := Logger{
		IncludesNativeInfo: false,
	}

	if status == "" || status == "enabled" {
		logger.Enabled = true
	} else {
		logger.Enabled = false
	}

	if logger.Enabled {
		serverEnv := os.Getenv("OPEN_RUNTIMES_ENV")

		if serverEnv == "development" {
			logger.Id = "dev"
		} else {
			if id == "" {
				logger.Id = logger._GenerateId()
			} else {
				logger.Id = id
			}
		}

		fileLogs, err := os.OpenFile("/mnt/logs/"+logger.Id+"_logs.log", os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0600)
		if err != nil {
			return Logger{}, errors.New("could not prepare log file")
		}
		logger.StreamLogs = fileLogs

		fileErrors, err := os.OpenFile("/mnt/logs/"+logger.Id+"_errors.log", os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0600)
		if err != nil {
			return Logger{}, errors.New("could not prepare log file")
		}
		logger.StreamErrors = fileErrors
	}

	return logger, nil
}

func (l Logger) Write(message interface{}, xtype string, xnative bool) {
	if xnative && !l.IncludesNativeInfo {
		l.IncludesNativeInfo = true
		l.Write("Native logs detected. Use context.Log() or context.Error() for better experience.", xtype, xnative)
	}

	stream := l.StreamLogs

	if xtype == LOGGER_TYPE_ERROR {
		stream = l.StreamErrors
	}

	stringLog := ""

	switch message.(type) {
	case string:
		stringLog = message.(string)
	case Log:
		log := message.(Log)
		stringLog = log.String()
	default:
		jsonData, err := json.Marshal(message)
		if err != nil {
			stringLog = fmt.Sprintf("%v", message)
		} else {
			jsonString := string(jsonData)
			stringLog = jsonString
		}
	}

	stream.Write([]byte(stringLog))
}

func (l Logger) End() {
	if !l.Enabled {
		return
	}

	l.Enabled = false

	l.StreamLogs.Close()
	l.StreamErrors.Close()
}

func (l Logger) OverrideNativeLogs() error {
	l.NativeLogsCache = os.Stdout
	l.NativeErrorsCache = os.Stderr

	readerLogs, writerLogs, errLogs := os.Pipe()
	if errLogs != nil {
		return errors.New("could not prepare log capturing")
	}
	l.WriterLogs = writerLogs

	readerErrors, writerErrors, errErrors := os.Pipe()
	if errErrors != nil {
		return errors.New("could not prepare log capturing")
	}
	l.WriterErrors = writerErrors

	os.Stdout = writerLogs
	os.Stderr = writerErrors

	log.SetOutput(writerLogs)

	l.NativeStreamLogs = make(chan string)
	go func() {
		var buf bytes.Buffer
		io.Copy(&buf, readerLogs)
		l.NativeStreamLogs <- buf.String()
	}()

	l.NativeStreamErrors = make(chan string)
	go func() {
		var buf bytes.Buffer
		io.Copy(&buf, readerErrors)
		l.NativeStreamErrors <- buf.String()
	}()

	return nil
}

func (l Logger) RevertNativeLogs() {
	l.WriterLogs.Close()
	l.WriterErrors.Close()

	os.Stdout = l.NativeLogsCache
	os.Stderr = l.NativeErrorsCache
	log.SetOutput(os.Stderr)

	customLogs := <-l.NativeStreamLogs
	if customLogs != "" {
		l.Write(customLogs, LOGGER_TYPE_LOG, true)
	}

	customErrors := <-l.NativeStreamErrors
	if customErrors != "" {
		l.Write(customLogs, LOGGER_TYPE_ERROR, true)
	}
}

func (l Logger) _GenerateId() string {
	timestamp := time.Now().UnixNano()
	randomNumber := rand.Intn(1000)

	// TODO: Improve logic, add padding
	uniqueID := fmt.Sprintf("%d%d", timestamp, randomNumber)
	return uniqueID
}
