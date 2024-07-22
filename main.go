package openruntimes

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"time"
)

const LOGGER_TYPE_LOG = "log"
const LOGGER_TYPE_ERROR = "error"

type Context struct {
	logger Logger

	Req ContextRequest
	Res ContextResponse
}

func NewContext(logger Logger) Context {
	return Context{
		logger: logger,
	}
}

type Log struct {
	Message string
}

func (l Log) String() string {
	return l.Message
}

func (c *Context) Log(message interface{}) {
	switch v := message.(type) {
	default:
		c.logger.Write(fmt.Sprintf("%#v", v)+"\n", LOGGER_TYPE_LOG, false)
	case string:
		c.logger.Write(v+"\n", LOGGER_TYPE_LOG, false)
	}
}

func (c *Context) Error(message interface{}) {
	switch v := message.(type) {
	default:
		c.logger.Write(fmt.Sprintf("%#v", v)+"\n", LOGGER_TYPE_ERROR, false)
	case string:
		c.logger.Write(v+"\n", LOGGER_TYPE_ERROR, false)
	}
}

type ContextRequest struct {
	bodyBinary  []byte
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

func (r *ContextRequest) SetBodyBinary(bytes []byte) {
	r.bodyBinary = bytes
}

func (r ContextRequest) BodyBinary() []byte {
	return r.bodyBinary
}

func (r ContextRequest) BodyText() string {
	return string(r.BodyBinary())
}

func (r ContextRequest) BodyRaw() string {
	return r.BodyText()
}

func (r ContextRequest) BodyJson(v any) error {
	bodyBinary := r.BodyBinary()

	err := json.Unmarshal(bodyBinary, v)

	if err != nil {
		return errors.New("could not parse body into a JSON")
	}

	return nil
}

func (r ContextRequest) Body() interface{} {
	contentType := r.Headers["content-type"]

	if contentType == "application/json" {
		if len(r.bodyBinary) == 0 {
			return map[string]interface{}{}
		}

		var bodyJson map[string]interface{}
		err := r.BodyJson(&bodyJson)

		if err != nil {
			return map[string]interface{}{}
		}

		return bodyJson
	}

	binaryTypes := []string{"application/", "audio/", "font/", "image/", "video/"}
	for _, binaryType := range binaryTypes {
		if strings.HasPrefix(contentType, binaryType) {
			return r.BodyBinary()
		}
	}

	return r.BodyText()
}

type Response struct {
	Body       []byte
	StatusCode int
	Headers    map[string]string

	enabledSetters map[string]bool
}

func (r Response) New() *Response {
	r.enabledSetters = map[string]bool{
		"Body":       false,
		"StatusCode": false,
		"Headers":    false,
	}
	return &r
}

type ResponseOption func(*Response)

type ContextResponse struct{}

func (r ContextResponse) WithHeaders(headers map[string]string) ResponseOption {
	return func(o *Response) {
		o.Headers = headers
		o.enabledSetters["Headers"] = true
	}
}

func (r ContextResponse) WithStatusCode(statusCode int) ResponseOption {
	return func(o *Response) {
		o.StatusCode = statusCode
		o.enabledSetters["StatusCode"] = true
	}
}

func (r ContextResponse) Binary(bytes []byte, optionalSetters ...ResponseOption) Response {
	options := Response{}.New()
	for _, opt := range optionalSetters {
		opt(options)
	}

	statusCode := 200
	headers := map[string]string{}

	if options.enabledSetters["Headers"] {
		headers = options.Headers
	}

	if options.enabledSetters["StatusCode"] {
		statusCode = options.StatusCode
	}

	return Response{
		Body:       bytes,
		StatusCode: statusCode,
		Headers:    headers,
	}
}

func (r ContextResponse) Send(body string, optionalSetters ...ResponseOption) Response {
	return r.Text(body, optionalSetters...)
}

func (r ContextResponse) Text(body string, optionalSetters ...ResponseOption) Response {
	return r.Binary([]byte(body), optionalSetters...)
}

func (r ContextResponse) Json(bodyStruct interface{}, optionalSetters ...ResponseOption) Response {
	options := Response{}.New()
	for _, opt := range optionalSetters {
		opt(options)
	}

	headers := map[string]string{}
	if options.enabledSetters["Headers"] {
		headers = options.Headers
	}

	headers["content-type"] = "application/json"
	optionalSetters = append(optionalSetters, r.WithHeaders(headers))

	jsonData, err := json.Marshal(bodyStruct)
	if err != nil {
		optionalSetters = append(optionalSetters, r.WithStatusCode(500))
		return r.Text("Error encoding JSON.", optionalSetters...)
	}

	jsonString := string(jsonData[:])

	return r.Text(jsonString, optionalSetters...)
}

func (r ContextResponse) Empty() Response {
	return r.Text("", r.WithStatusCode(204))
}

func (r ContextResponse) Redirect(url string, optionalSetters ...ResponseOption) Response {
	options := Response{}.New()
	for _, opt := range optionalSetters {
		opt(options)
	}

	headers := map[string]string{}
	if options.enabledSetters["Headers"] {
		headers = options.Headers
	}

	headers["location"] = url
	optionalSetters = append(optionalSetters, r.WithHeaders(headers))

	optionalSetters = append([]ResponseOption{r.WithStatusCode(301)}, optionalSetters...)

	return r.Text("", optionalSetters...)
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
		logger.Id = ""
	}

	if logger.Enabled {
		serverEnv := os.Getenv("OPEN_RUNTIMES_ENV")

		if id == "" {
			if serverEnv == "development" {
				logger.Id = "dev"
			} else {
				logger.Id = logger.generateId(7)
			}
		} else {
			logger.Id = id
		}

		fileLogs, err := os.OpenFile("/mnt/logs/"+logger.Id+"_logs.log", os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
		if err != nil {
			return Logger{}, errors.New("could not prepare log file")
		}
		logger.StreamLogs = fileLogs

		fileErrors, err := os.OpenFile("/mnt/logs/"+logger.Id+"_errors.log", os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
		if err != nil {
			return Logger{}, errors.New("could not prepare log file")
		}
		logger.StreamErrors = fileErrors
	}

	return logger, nil
}

func (l *Logger) Write(message interface{}, xtype string, xnative bool) {
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

func (l *Logger) End() {
	if !l.Enabled {
		return
	}

	l.Enabled = false

	l.StreamLogs.Close()
	l.StreamErrors.Close()
}

func (l *Logger) OverrideNativeLogs() error {
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

	log.SetOutput(writerErrors)

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

func (l *Logger) RevertNativeLogs() {
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

func (l Logger) generateId(padding int) string {
	timestamp := time.Now().UnixNano() / 1000

	choices := []string{"0", "1", "2", "3", "4", "5", "6", "7", "8", "9", "a", "b", "c", "d", "e", "f"}
	hexString := strconv.FormatInt(timestamp, 16)

	if padding > 0 {
		for i := 0; i < padding; i++ {
			hexString += choices[rand.Intn(len(choices))]
		}
	}

	return hexString
}
