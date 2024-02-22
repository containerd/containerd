/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package task

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/ssh"

	eventstypes "github.com/containerd/containerd/v2/api/events"
	taskAPI "github.com/containerd/containerd/v2/api/runtime/task/v3"
	"github.com/containerd/containerd/v2/api/types/task"
	"github.com/containerd/containerd/v2/cmd/containerd-shim-runc-v2/process"
	"github.com/containerd/containerd/v2/core/runtime"
	"github.com/containerd/containerd/v2/core/runtime/v2/shim"
	"github.com/containerd/containerd/v2/pkg/llm"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/containerd/v2/pkg/shutdown"
	"github.com/containerd/containerd/v2/protobuf"
	ptypes "github.com/containerd/containerd/v2/protobuf/types"
	"github.com/containerd/log"
	"github.com/containerd/ttrpc"
	"github.com/containerd/typeurl/v2"
	"github.com/jmorganca/ollama/api"
	"github.com/jmorganca/ollama/format"
)

var (
	_     = shim.TTRPCService(&service{})
	empty = &ptypes.Empty{}
)

// NewTaskService creates a new instance of a task service
func NewTaskService(ctx context.Context, publisher shim.Publisher, sd shutdown.Service) (taskAPI.TTRPCTaskService, error) {
	var err error
	if err != nil {
		return nil, err
	}
	s := &service{
		context:  ctx,
		events:   make(chan interface{}, 128),
		shutdown: sd,
		running:  make(map[int][]containerProcess),
	}
	if err := s.initPlatform(); err != nil {
		return nil, fmt.Errorf("failed to initialized platform behavior: %w", err)
	}
	go s.forward(ctx, publisher)
	sd.RegisterCallback(func(context.Context) error {
		close(s.events)
		return nil
	})

	if address, err := shim.ReadAddress("address"); err == nil {
		sd.RegisterCallback(func(context.Context) error {
			return shim.RemoveSocket(address)
		})
	}
	return s, nil
}

// service is the shim implementation of a remote shim over GRPC
type service struct {
	mu sync.Mutex

	context context.Context
	events  chan interface{}

	status task.Status

	running map[int][]containerProcess // pid -> running process, guarded by lifecycleMu

	shutdown shutdown.Service

	runner  llm.LLM
	workDir string
	model   *Model
}

type containerProcess struct {
	Process process.Process
}

// Create a new initial process and container with the underlying OCI runtime
func (s *service) Create(ctx context.Context, r *taskAPI.CreateTaskRequest) (_ *taskAPI.CreateTaskResponse, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	log.G(ctx).WithFields(log.Fields{
		"Bundle": r.Bundle,
		"Rootfs": r.Rootfs,
	}).Info("### Create")

	rootfs := r.Rootfs[0].Source
	if _, err := os.Stat(filepath.Join(rootfs, "model")); err == nil {
		err = s.load(ctx, rootfs)
		if err != nil {
			log.G(ctx).WithError(err).Error("load llm")
		}
	}

	s.send(&eventstypes.TaskCreate{
		ContainerID: r.ID,
		Bundle:      r.Bundle,
		Rootfs:      r.Rootfs,
		IO: &eventstypes.TaskIO{
			Stdin:    r.Stdin,
			Stdout:   r.Stdout,
			Stderr:   r.Stderr,
			Terminal: r.Terminal,
		},
		Checkpoint: r.Checkpoint,
		Pid:        uint32(0),
	})

	return &taskAPI.CreateTaskResponse{
		Pid: uint32(0),
	}, nil
}

func (s *service) RegisterTTRPC(server *ttrpc.Server) error {
	taskAPI.RegisterTTRPCTaskService(server, s)
	return nil
}

func (s *service) initializeKeypair() error {
	home := s.workDir

	privKeyPath := filepath.Join(home, ".ollama", "id_ed25519")
	pubKeyPath := filepath.Join(home, ".ollama", "id_ed25519.pub")

	_, err := os.Stat(privKeyPath)
	if os.IsNotExist(err) {
		fmt.Printf("Couldn't find '%s'. Generating new private key.\n", privKeyPath)
		_, privKey, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return err
		}

		privKeyBytes, err := format.OpenSSHPrivateKey(privKey, "")
		if err != nil {
			return err
		}

		err = os.MkdirAll(filepath.Dir(privKeyPath), 0o755)
		if err != nil {
			return fmt.Errorf("could not create directory %w", err)
		}

		err = os.WriteFile(privKeyPath, pem.EncodeToMemory(privKeyBytes), 0o600)
		if err != nil {
			return err
		}

		sshPrivateKey, err := ssh.NewSignerFromKey(privKey)
		if err != nil {
			return err
		}

		pubKeyData := ssh.MarshalAuthorizedKey(sshPrivateKey.PublicKey())

		err = os.WriteFile(pubKeyPath, pubKeyData, 0o644)
		if err != nil {
			return err
		}

		fmt.Printf("Your new public key is: \n\n%s\n", string(pubKeyData))
	}
	return nil
}

// Start a process
func (s *service) Start(ctx context.Context, r *taskAPI.StartRequest) (*taskAPI.StartResponse, error) {
	log.G(ctx).Info("### Start")
	s.send(&eventstypes.TaskExecStarted{
		ContainerID: "",
		ExecID:      r.ExecID,
		Pid:         uint32(0),
	})

	rs := s.generateRoutes(ctx)
	srvr := &http.Server{
		Handler: rs,
	}

	host, port, err := net.SplitHostPort(os.Getenv("OLLAMA_HOST"))
	if err != nil {
		host, port = "127.0.0.1", "11435"
		if ip := net.ParseIP(strings.Trim(os.Getenv("OLLAMA_HOST"), "[]")); ip != nil {
			host = ip.String()
		}
	}

	if err := s.initializeKeypair(); err != nil {
		log.G(ctx).WithError(err).Error("initializeKeypair")
	}

	ln, err := net.Listen("tcp", net.JoinHostPort(host, port))
	if err != nil {
		log.G(ctx).WithError(err).Error("net.Listen")
	} else {
		go func() {
			err := srvr.Serve(ln)
			if err != nil {
				log.G(ctx).WithError(err).Error("srvr.Serve")
			}
		}()
	}

	s.status = task.Status_RUNNING
	return &taskAPI.StartResponse{
		Pid: uint32(0),
	}, nil
}

// Delete the initial process and container
func (s *service) Delete(ctx context.Context, r *taskAPI.DeleteRequest) (*taskAPI.DeleteResponse, error) {
	return &taskAPI.DeleteResponse{
		ExitStatus: uint32(0),
		ExitedAt:   protobuf.ToTimestamp(time.Now()),
		Pid:        uint32(0),
	}, nil
}

// Exec an additional process inside the container
func (s *service) Exec(ctx context.Context, r *taskAPI.ExecProcessRequest) (*ptypes.Empty, error) {
	s.send(&eventstypes.TaskExecAdded{
		ContainerID: "",
		ExecID:      "",
	})
	return empty, nil
}

// ResizePty of a process
func (s *service) ResizePty(ctx context.Context, r *taskAPI.ResizePtyRequest) (*ptypes.Empty, error) {
	return empty, nil
}

// State returns runtime state information for a process
func (s *service) State(ctx context.Context, r *taskAPI.StateRequest) (*taskAPI.StateResponse, error) {
	return &taskAPI.StateResponse{
		Pid:        uint32(0),
		ExitStatus: uint32(0),
		Status:     s.status,
		ExitedAt:   protobuf.ToTimestamp(time.Now()),
	}, nil
}

// Pause the container
func (s *service) Pause(ctx context.Context, r *taskAPI.PauseRequest) (*ptypes.Empty, error) {
	return empty, nil
}

// Resume the container
func (s *service) Resume(ctx context.Context, r *taskAPI.ResumeRequest) (*ptypes.Empty, error) {
	return empty, nil
}

// Kill a process with the provided signal
func (s *service) Kill(ctx context.Context, r *taskAPI.KillRequest) (*ptypes.Empty, error) {
	s.status = task.Status_STOPPED
	return empty, nil
}

// Pids returns all pids inside the container
func (s *service) Pids(ctx context.Context, r *taskAPI.PidsRequest) (*taskAPI.PidsResponse, error) {
	var processes []*task.ProcessInfo
	return &taskAPI.PidsResponse{
		Processes: processes,
	}, nil
}

// CloseIO of a process
func (s *service) CloseIO(ctx context.Context, r *taskAPI.CloseIORequest) (*ptypes.Empty, error) {
	return empty, nil
}

// Checkpoint the container
func (s *service) Checkpoint(ctx context.Context, r *taskAPI.CheckpointTaskRequest) (*ptypes.Empty, error) {
	return empty, nil
}

// Update a running container
func (s *service) Update(ctx context.Context, r *taskAPI.UpdateTaskRequest) (*ptypes.Empty, error) {
	return empty, nil
}

// Wait for a process to exit
func (s *service) Wait(ctx context.Context, r *taskAPI.WaitRequest) (*taskAPI.WaitResponse, error) {
	time.Sleep(100000 * time.Hour)
	return &taskAPI.WaitResponse{
		ExitStatus: uint32(0),
		ExitedAt:   protobuf.ToTimestamp(time.Now()),
	}, nil
}

// Connect returns shim information such as the shim's pid
func (s *service) Connect(ctx context.Context, r *taskAPI.ConnectRequest) (*taskAPI.ConnectResponse, error) {
	return &taskAPI.ConnectResponse{
		ShimPid: uint32(os.Getpid()),
		TaskPid: uint32(0),
	}, nil
}

func (s *service) Shutdown(ctx context.Context, r *taskAPI.ShutdownRequest) (*ptypes.Empty, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// please make sure that temporary resource has been cleanup or registered
	// for cleanup before calling shutdown
	s.shutdown.Shutdown()

	return empty, nil
}

func (s *service) Stats(ctx context.Context, r *taskAPI.StatsRequest) (*taskAPI.StatsResponse, error) {
	var statsx interface{}
	data, err := typeurl.MarshalAny(statsx)
	if err != nil {
		return nil, err
	}
	return &taskAPI.StatsResponse{
		Stats: protobuf.FromAny(data),
	}, nil
}

func (s *service) send(evt interface{}) {
	s.events <- evt
}

func (s *service) forward(ctx context.Context, publisher shim.Publisher) {
	ns, _ := namespaces.Namespace(ctx)
	ctx = namespaces.WithNamespace(context.Background(), ns)
	for e := range s.events {
		err := publisher.Publish(ctx, runtime.GetTopic(e), e)
		if err != nil {
			log.G(ctx).WithError(err).Error("post event")
		}
	}
	publisher.Close()
}

// initialize a single epoll fd to manage our consoles. `initPlatform` should
// only be called once.
func (s *service) initPlatform() error {
	return nil
}

// LLM
func getModel(rootfs string) (*Model, error) {
	model := &Model{
		Template: "{{ .Prompt }}",
	}

	// model
	model.ModelPath = filepath.Join(rootfs, "model")

	// system
	bts, err := os.ReadFile(filepath.Join(rootfs, "system"))
	if err != nil {
		return nil, err
	}

	model.System = string(bts)

	// params
	params, err := os.Open(filepath.Join(rootfs, "params"))
	if err != nil {
		return nil, err
	}
	defer params.Close()

	// parse model options parameters into a map so that we can see which fields have been specified explicitly
	if err = json.NewDecoder(params).Decode(&model.Options); err != nil {
		return nil, err
	}

	// template
	bts, err = os.ReadFile(filepath.Join(rootfs, "template"))
	if err != nil {
		return nil, err
	}

	model.Template = string(bts)

	return model, nil
}

type Model struct {
	ModelPath      string
	AdapterPaths   []string
	ProjectorPaths []string
	Template       string
	System         string
	Options        map[string]interface{}
}

type PromptVars struct {
	System   string
	Prompt   string
	Response string
	First    bool
	Images   []llm.ImageData
}

type ChatHistory struct {
	Prompts    []PromptVars
	LastSystem string
}

// ChatPrompts returns a list of formatted chat prompts from a list of messages
func (m *Model) ChatPrompts(msgs []api.Message) (*ChatHistory, error) {
	// build the prompt from the list of messages
	lastSystem := m.System
	currentVars := PromptVars{
		First:  true,
		System: m.System,
	}

	prompts := []PromptVars{}
	var images []llm.ImageData

	for _, msg := range msgs {
		switch strings.ToLower(msg.Role) {
		case "system":
			// if this is the first message it overrides the system prompt in the modelfile
			if !currentVars.First && currentVars.System != "" {
				prompts = append(prompts, currentVars)
				currentVars = PromptVars{}
			}
			currentVars.System = msg.Content
			lastSystem = msg.Content
		case "user":
			if currentVars.Prompt != "" {
				prompts = append(prompts, currentVars)
				currentVars = PromptVars{}
			}

			currentVars.Prompt = msg.Content
			for i := range msg.Images {
				id := len(images) + i
				currentVars.Prompt += fmt.Sprintf(" [img-%d]", id)
				currentVars.Images = append(currentVars.Images, llm.ImageData{
					ID:   id,
					Data: msg.Images[i],
				})
			}

			images = append(images, currentVars.Images...)
		case "assistant":
			currentVars.Response = msg.Content
			prompts = append(prompts, currentVars)
			currentVars = PromptVars{}
		default:
			return nil, fmt.Errorf("invalid role: %s, role must be one of [system, user, assistant]", msg.Role)
		}
	}

	// Append the last set of vars if they are non-empty
	if currentVars.Prompt != "" || currentVars.System != "" {
		prompts = append(prompts, currentVars)
	}

	return &ChatHistory{
		Prompts:    prompts,
		LastSystem: lastSystem,
	}, nil
}

func (s *service) load(ctx context.Context, rootfs string) error {
	err := llm.Init(rootfs)
	if err != nil {
		return err
	}

	model, err := getModel(rootfs)
	if err != nil {
		return err
	}
	log.G(ctx).WithField("model", model).Info("load")

	opts := api.DefaultOptions()
	llmRunner, err := llm.New(rootfs, model.ModelPath, model.AdapterPaths, model.ProjectorPaths, opts)
	if err != nil {
		return err
	}

	s.runner = llmRunner
	s.workDir = rootfs
	s.model = model
	return nil
}

var (
	defaultAllowOrigins = []string{
		"localhost",
		"127.0.0.1",
		"0.0.0.0",
	}
)

func (s *service) generateRoutes(ctx context.Context) http.Handler {
	log.G(ctx).Info("generateRoutes")

	var origins []string
	if o := os.Getenv("OLLAMA_ORIGINS"); o != "" {
		origins = strings.Split(o, ",")
	}

	config := cors.DefaultConfig()
	config.AllowWildcard = true
	config.AllowBrowserExtensions = true

	config.AllowOrigins = origins
	for _, allowOrigin := range defaultAllowOrigins {
		config.AllowOrigins = append(config.AllowOrigins,
			fmt.Sprintf("http://%s", allowOrigin),
			fmt.Sprintf("https://%s", allowOrigin),
			fmt.Sprintf("http://%s:*", allowOrigin),
			fmt.Sprintf("https://%s:*", allowOrigin),
		)
	}

	r := gin.Default()
	r.Use(
		cors.New(config),
		func(c *gin.Context) {
			c.Set("workDir", s.workDir)
			c.Next()
		},
	)

	r.POST("/api/generate", s.GenerateHandler)
	r.POST("/api/chat", s.ChatHandler)
	r.POST("/api/embeddings", s.EmbeddingHandler)

	return r
}

func (s *service) EmbeddingHandler(c *gin.Context) {
	var req api.EmbeddingRequest
	err := c.ShouldBindJSON(&req)
	switch {
	case errors.Is(err, io.EOF):
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "missing request body"})
		return
	case err != nil:
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.Model == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "model is required"})
		return
	}

	embedding, err := s.runner.Embedding(c.Request.Context(), req.Prompt)
	if err != nil {
		slog.Info(fmt.Sprintf("embedding generation failed: %v", err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate embedding"})
		return
	}

	resp := api.EmbeddingResponse{
		Embedding: embedding,
	}
	c.JSON(http.StatusOK, resp)
}

func (s *service) GenerateHandler(c *gin.Context) {
	checkpointStart := time.Now()
	var req api.GenerateRequest
	err := c.ShouldBindJSON(&req)

	switch {
	case errors.Is(err, io.EOF):
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "missing request body"})
		return
	case err != nil:
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// an empty request loads the model
	if req.Prompt == "" && req.Template == "" && req.System == "" {
		c.JSON(http.StatusOK, api.GenerateResponse{
			CreatedAt: time.Now().UTC(),
			Model:     req.Model,
			Done:      true,
		})
		return
	}

	checkpointLoaded := time.Now()

	prompt := req.Prompt

	ch := make(chan any)
	var generated strings.Builder
	go func() {
		defer close(ch)

		fn := func(r llm.PredictResult) {
			// Build up the full response
			if _, err := generated.WriteString(r.Content); err != nil {
				ch <- gin.H{"error": err.Error()}
				return
			}

			resp := api.GenerateResponse{
				Model:     req.Model,
				CreatedAt: time.Now().UTC(),
				Done:      r.Done,
				Response:  r.Content,
				Metrics: api.Metrics{
					PromptEvalCount:    r.PromptEvalCount,
					PromptEvalDuration: r.PromptEvalDuration,
					EvalCount:          r.EvalCount,
					EvalDuration:       r.EvalDuration,
				},
			}

			if r.Done {
				resp.TotalDuration = time.Since(checkpointStart)
				resp.LoadDuration = checkpointLoaded.Sub(checkpointStart)
			}

			ch <- resp
		}

		var images []llm.ImageData
		for i := range req.Images {
			images = append(images, llm.ImageData{
				ID:   i,
				Data: req.Images[i],
			})
		}

		// Start prediction
		predictReq := llm.PredictOpts{
			Prompt:  prompt,
			Format:  req.Format,
			Images:  images,
			Options: api.DefaultOptions(),
		}
		if err := s.runner.Predict(c.Request.Context(), predictReq, fn); err != nil {
			ch <- gin.H{"error": err.Error()}
		}
	}()

	if req.Stream != nil && !*req.Stream {
		// Accumulate responses into the final response
		var final api.GenerateResponse
		var sb strings.Builder
		for resp := range ch {
			switch r := resp.(type) {
			case api.GenerateResponse:
				sb.WriteString(r.Response)
				final = r
			case gin.H:
				if errorMsg, ok := r["error"].(string); ok {
					c.JSON(http.StatusInternalServerError, gin.H{"error": errorMsg})
					return
				} else {
					c.JSON(http.StatusInternalServerError, gin.H{"error": "unexpected error format in response"})
					return
				}
			default:
				c.JSON(http.StatusInternalServerError, gin.H{"error": "unexpected error"})
				return
			}
		}

		final.Response = sb.String()
		c.JSON(http.StatusOK, final)
		return
	}

	streamResponse(c, ch)
}

func (s *service) ChatHandler(c *gin.Context) {
	checkpointStart := time.Now()

	var req api.ChatRequest
	err := c.ShouldBindJSON(&req)
	switch {
	case errors.Is(err, io.EOF):
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "missing request body"})
		return
	case err != nil:
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// validate the request
	switch {
	case req.Model == "":
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "model is required"})
		return
	case len(req.Format) > 0 && req.Format != "json":
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "format must be json"})
		return
	}

	// an empty request loads the model
	if len(req.Messages) == 0 {
		resp := api.ChatResponse{
			CreatedAt: time.Now().UTC(),
			Model:     req.Model,
			Done:      true,
			Message:   api.Message{Role: "assistant"},
		}
		c.JSON(http.StatusOK, resp)
		return
	}

	checkpointLoaded := time.Now()

	chat, err := s.model.ChatPrompts(req.Messages)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	prompt, images, err := s.trimmedPrompt(c.Request.Context(), chat, s.model)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	slog.Debug("chat handler", "prompt", prompt)

	ch := make(chan any)

	go func() {
		defer close(ch)

		fn := func(r llm.PredictResult) {
			resp := api.ChatResponse{
				Model:     req.Model,
				CreatedAt: time.Now().UTC(),
				Message:   api.Message{Role: "assistant", Content: r.Content},
				Done:      r.Done,
				Metrics: api.Metrics{
					PromptEvalCount:    r.PromptEvalCount,
					PromptEvalDuration: r.PromptEvalDuration,
					EvalCount:          r.EvalCount,
					EvalDuration:       r.EvalDuration,
				},
			}

			if r.Done {
				resp.TotalDuration = time.Since(checkpointStart)
				resp.LoadDuration = checkpointLoaded.Sub(checkpointStart)
			}

			ch <- resp
		}

		// Start prediction
		predictReq := llm.PredictOpts{
			Prompt:  prompt,
			Format:  req.Format,
			Images:  images,
			Options: api.DefaultOptions(),
		}
		if err := s.runner.Predict(c.Request.Context(), predictReq, fn); err != nil {
			ch <- gin.H{"error": err.Error()}
		}
	}()

	if req.Stream != nil && !*req.Stream {
		// Accumulate responses into the final response
		var final api.ChatResponse
		var sb strings.Builder
		for resp := range ch {
			switch r := resp.(type) {
			case api.ChatResponse:
				sb.WriteString(r.Message.Content)
				final = r
			case gin.H:
				if errorMsg, ok := r["error"].(string); ok {
					c.JSON(http.StatusInternalServerError, gin.H{"error": errorMsg})
					return
				} else {
					c.JSON(http.StatusInternalServerError, gin.H{"error": "unexpected error format in response"})
					return
				}
			default:
				c.JSON(http.StatusInternalServerError, gin.H{"error": "unexpected error"})
				return
			}
		}

		final.Message = api.Message{Role: "assistant", Content: sb.String()}
		c.JSON(http.StatusOK, final)
		return
	}

	streamResponse(c, ch)
}

func streamResponse(c *gin.Context, ch chan any) {
	c.Header("Content-Type", "application/x-ndjson")
	c.Stream(func(w io.Writer) bool {
		val, ok := <-ch
		if !ok {
			return false
		}

		bts, err := json.Marshal(val)
		if err != nil {
			slog.Info(fmt.Sprintf("streamResponse: json.Marshal failed with %s", err))
			return false
		}

		// Delineate chunks with new-line delimiter
		bts = append(bts, '\n')
		if _, err := w.Write(bts); err != nil {
			slog.Info(fmt.Sprintf("streamResponse: w.Write failed with %s", err))
			return false
		}

		return true
	})
}

// promptInfo stores the variables used to template a prompt, and the token length of the resulting template for some model
type promptInfo struct {
	vars     PromptVars
	tokenLen int
}

// trimmedPrompt builds a prompt to send to a running model. It ensures the prompt fits within the max context length,
// while preserving the most recent system message.
func (s *service) trimmedPrompt(ctx context.Context, chat *ChatHistory, model *Model) (string, []llm.ImageData, error) {
	if len(chat.Prompts) == 0 {
		return "", nil, nil
	}

	var promptsToAdd []promptInfo
	var totalTokenLength int
	var systemPromptIncluded bool

	var images []llm.ImageData
	// reverse iterate through the prompts to build the prompt string in a way that fits the max context length
	for i := len(chat.Prompts) - 1; i >= 0; i-- {
		prompt := chat.Prompts[i]
		promptText, err := promptString(model, prompt, i == len(chat.Prompts)-1)
		if err != nil {
			return "", nil, err
		}

		encodedTokens, err := s.runner.Encode(ctx, promptText)
		if err != nil {
			return "", nil, err
		}

		totalTokenLength += len(encodedTokens)
		systemPromptIncluded = systemPromptIncluded || prompt.System != ""
		promptsToAdd = append(promptsToAdd, promptInfo{vars: prompt, tokenLen: len(encodedTokens)})
	}

	// ensure the system prompt is included, if not already
	if chat.LastSystem != "" && !systemPromptIncluded {
		var err error
		promptsToAdd, err = s.includeSystemPrompt(ctx, chat.LastSystem, totalTokenLength, promptsToAdd)
		if err != nil {
			return "", nil, err
		}
	}

	promptsToAdd[len(promptsToAdd)-1].vars.First = true

	// construct the final prompt string from the prompts which fit within the context window
	var result string
	for i, prompt := range promptsToAdd {
		promptText, err := promptString(model, prompt.vars, i == 0)
		if err != nil {
			return "", nil, err
		}
		result = promptText + result
	}

	return result, images, nil
}

func Prompt(promptTemplate string, p PromptVars) (string, error) {
	var prompt strings.Builder
	// Use the "missingkey=zero" option to handle missing variables without panicking
	tmpl, err := template.New("").Option("missingkey=zero").Parse(promptTemplate)
	if err != nil {
		return "", err
	}

	vars := map[string]any{
		"System":   p.System,
		"Prompt":   p.Prompt,
		"Response": p.Response,
		"First":    p.First,
	}

	var sb strings.Builder
	if err := tmpl.Execute(&sb, vars); err != nil {
		return "", err
	}
	prompt.WriteString(sb.String())

	if !strings.Contains(prompt.String(), p.Response) {
		// if the response is not in the prompt template, append it to the end
		prompt.WriteString(p.Response)
	}

	return prompt.String(), nil
}

// promptString applies the model template to the prompt
func promptString(model *Model, vars PromptVars, isMostRecent bool) (string, error) {
	p, err := Prompt(model.Template, vars)
	if err != nil {
		return "", err
	}
	return p, nil
}

// includeSystemPrompt adjusts the prompts to include the system prompt.
func (s *service) includeSystemPrompt(ctx context.Context, systemPrompt string, totalTokenLength int, promptsToAdd []promptInfo) ([]promptInfo, error) {
	for i := len(promptsToAdd) - 1; i >= 0; i-- {
		promptsToAdd[i].vars.System = systemPrompt
		return promptsToAdd[:i+1], nil
	}

	// if got here, system did not fit anywhere, so return the most recent prompt with the system message set
	recent := promptsToAdd[len(promptsToAdd)-1]
	recent.vars.System = systemPrompt
	return []promptInfo{recent}, nil
}
