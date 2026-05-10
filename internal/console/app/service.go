package app

import (
	"backend-core/internal/instance/domain"
	"backend-core/pkg/contracts"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"
)

const (
	sessionTTL     = 60 * time.Second
	streamWait     = 15 * time.Second
	agentQueueSize = 128
)

var (
	ErrSessionNotFound    = errors.New("console session not found")
	ErrSessionExpired     = errors.New("console session expired")
	ErrAgentUnavailable   = errors.New("console agent unavailable")
	ErrRuntimeUnavailable = errors.New("console runtime state unavailable")
)

type InstanceRepository interface {
	GetByID(id string) (*domain.Instance, error)
}

type RuntimeStateReader interface {
	GetInstanceRuntimeState(instanceID, nodeID string) (contracts.InstanceRuntimeState, bool)
}

type Session struct {
	ID         string
	Ticket     string
	VncTicket  string
	InstanceID string
	NodeID     string
	UserID     string
	ExpiresAt  time.Time
	agent      *AgentStream
	browser    bool
}

type Service struct {
	instances InstanceRepository
	runtime   RuntimeStateReader

	mu       sync.Mutex
	sessions map[string]*Session
	pending  map[string]map[string]contracts.ConsoleSession
}

func NewService(instances InstanceRepository, runtime RuntimeStateReader) *Service {
	s := &Service{
		instances: instances,
		runtime:   runtime,
		sessions:  map[string]*Session{},
		pending:   map[string]map[string]contracts.ConsoleSession{},
	}
	go s.cleanupLoop()
	return s
}

func (s *Service) CreateSession(instanceID, userID string, admin bool) (*Session, error) {
	inst, err := s.instances.GetByID(instanceID)
	if err != nil {
		return nil, err
	}
	if !admin && inst.CustomerID() != userID {
		return nil, fmt.Errorf("console access denied")
	}
	if inst.NodeID() == "" {
		return nil, ErrRuntimeUnavailable
	}
	if inst.ControlStatus() != domain.InstanceControlStatusActive {
		return nil, fmt.Errorf("console requires active instance")
	}
	runtime, ok := s.runtime.GetInstanceRuntimeState(inst.ID(), inst.NodeID())
	if !ok || runtime.State != domain.InstanceStatusRunning {
		return nil, ErrRuntimeUnavailable
	}
	sessionID, err := randomToken(16)
	if err != nil {
		return nil, err
	}
	ticket, err := randomToken(32)
	if err != nil {
		return nil, err
	}
	session := &Session{
		ID:         sessionID,
		Ticket:     ticket,
		InstanceID: inst.ID(),
		NodeID:     inst.NodeID(),
		UserID:     userID,
		ExpiresAt:  time.Now().Add(sessionTTL),
	}
	s.mu.Lock()
	s.sessions[sessionID] = session
	if s.pending[session.NodeID] == nil {
		s.pending[session.NodeID] = map[string]contracts.ConsoleSession{}
	}
	s.pending[session.NodeID][session.ID] = contracts.ConsoleSession{
		SessionID:  session.ID,
		InstanceID: session.InstanceID,
	}
	s.mu.Unlock()

	return session, nil
}

func (s *Service) GetSession(sessionID, instanceID, userID string, admin bool) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, ok := s.sessions[sessionID]
	if !ok {
		return nil, ErrSessionNotFound
	}
	if time.Now().After(session.ExpiresAt) {
		delete(s.sessions, session.ID)
		if s.pending[session.NodeID] != nil {
			delete(s.pending[session.NodeID], session.ID)
			if len(s.pending[session.NodeID]) == 0 {
				delete(s.pending, session.NodeID)
			}
		}
		if session.agent != nil {
			session.agent.Close()
		}
		return nil, ErrSessionExpired
	}
	if session.InstanceID != instanceID {
		return nil, ErrSessionNotFound
	}
	if !admin && session.UserID != userID {
		return nil, fmt.Errorf("console access denied")
	}
	snapshot := *session
	return &snapshot, nil
}

func (s *Service) ClaimPendingSessions(nodeID string) []contracts.ConsoleSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := s.pending[nodeID]
	if len(items) == 0 {
		return nil
	}
	now := time.Now()
	out := make([]contracts.ConsoleSession, 0, len(items))
	for sessionID, session := range items {
		record := s.sessions[sessionID]
		if record == nil || now.After(record.ExpiresAt) {
			delete(items, sessionID)
			delete(s.sessions, sessionID)
			continue
		}
		out = append(out, session)
		delete(items, sessionID)
	}
	if len(items) == 0 {
		delete(s.pending, nodeID)
	}
	return out
}

func (s *Service) ConnectBrowser(ticket string) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, session := range s.sessions {
		if session.Ticket != ticket {
			continue
		}
		if time.Now().After(session.ExpiresAt) {
			delete(s.sessions, session.ID)
			return nil, ErrSessionExpired
		}
		session.Ticket = ""
		if session.browser {
			return nil, fmt.Errorf("console browser already connected")
		}
		session.browser = true
		return session, nil
	}
	return nil, ErrSessionNotFound
}

func (s *Service) WaitAgent(sessionID string) (*Session, *AgentStream, error) {
	deadline := time.Now().Add(streamWait)
	for {
		s.mu.Lock()
		session, ok := s.sessions[sessionID]
		if !ok {
			s.mu.Unlock()
			return nil, nil, ErrSessionNotFound
		}
		if time.Now().After(session.ExpiresAt) {
			delete(s.sessions, sessionID)
			s.mu.Unlock()
			return nil, nil, ErrSessionExpired
		}
		agent := session.agent
		s.mu.Unlock()
		if agent != nil {
			return session, agent, nil
		}
		if time.Now().After(deadline) {
			return nil, nil, ErrAgentUnavailable
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (s *Service) AttachAgent(nodeID string, stream contracts.ConsoleStream) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	if first.Control != "open" || first.SessionID == "" {
		return fmt.Errorf("console stream must start with open frame")
	}
	s.mu.Lock()
	session, ok := s.sessions[first.SessionID]
	if !ok {
		s.mu.Unlock()
		_ = stream.Send(contracts.ConsoleFrame{SessionID: first.SessionID, Error: ErrSessionNotFound.Error(), Control: "close"})
		return ErrSessionNotFound
	}
	if session.NodeID != nodeID {
		s.mu.Unlock()
		_ = stream.Send(contracts.ConsoleFrame{SessionID: first.SessionID, Error: "console node mismatch", Control: "close"})
		return fmt.Errorf("console node mismatch")
	}
	if time.Now().After(session.ExpiresAt) {
		delete(s.sessions, session.ID)
		s.mu.Unlock()
		_ = stream.Send(contracts.ConsoleFrame{SessionID: first.SessionID, Error: ErrSessionExpired.Error(), Control: "close"})
		return ErrSessionExpired
	}
	if session.agent != nil {
		s.mu.Unlock()
		_ = stream.Send(contracts.ConsoleFrame{SessionID: first.SessionID, Error: "console agent already connected", Control: "close"})
		return fmt.Errorf("console agent already connected")
	}
	agent := newAgentStream(stream)
	agent.onRecv = func(frame contracts.ConsoleFrame) bool {
		if frame.Control == "vnc_ticket" && len(frame.Data) > 0 {
			s.setVncTicket(session.ID, string(frame.Data))
			return false
		}
		return true
	}
	session.agent = agent
	s.mu.Unlock()

	defer s.CloseSession(session.ID)
	return agent.run()
}

func (s *Service) setVncTicket(sessionID, ticket string) {
	if ticket == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[sessionID]
	if !ok || session.VncTicket != "" {
		return
	}
	session.VncTicket = ticket
}

func (s *Service) CloseSession(sessionID string) {
	s.mu.Lock()
	session, ok := s.sessions[sessionID]
	if ok {
		delete(s.sessions, sessionID)
		if s.pending[session.NodeID] != nil {
			delete(s.pending[session.NodeID], sessionID)
			if len(s.pending[session.NodeID]) == 0 {
				delete(s.pending, session.NodeID)
			}
		}
	}
	s.mu.Unlock()
	if !ok {
		return
	}
	if session.agent != nil {
		session.agent.Close()
	}
}

func (s *Service) cleanupLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		var expired []*Session
		s.mu.Lock()
		for id, session := range s.sessions {
			if now.After(session.ExpiresAt) {
				expired = append(expired, session)
				delete(s.sessions, id)
				if s.pending[session.NodeID] != nil {
					delete(s.pending[session.NodeID], id)
					if len(s.pending[session.NodeID]) == 0 {
						delete(s.pending, session.NodeID)
					}
				}
			}
		}
		s.mu.Unlock()
		for _, session := range expired {
			if session.agent != nil {
				session.agent.Close()
			}
		}
	}
}

type AgentStream struct {
	stream contracts.ConsoleStream
	to     chan contracts.ConsoleFrame
	from   chan contracts.ConsoleFrame
	done   chan struct{}
	once   sync.Once
	onRecv func(frame contracts.ConsoleFrame) bool
}

func newAgentStream(stream contracts.ConsoleStream) *AgentStream {
	return &AgentStream{
		stream: stream,
		to:     make(chan contracts.ConsoleFrame, agentQueueSize),
		from:   make(chan contracts.ConsoleFrame, agentQueueSize),
		done:   make(chan struct{}),
	}
}

func (s *AgentStream) run() error {
	go func() {
		for {
			frame, err := s.stream.Recv()
			if err != nil {
				s.Close()
				return
			}
			if s.onRecv != nil && !s.onRecv(frame) {
				continue
			}
			select {
			case s.from <- frame:
			case <-s.done:
				return
			default:
				s.Close()
				return
			}
		}
	}()
	go func() {
		for {
			select {
			case frame := <-s.to:
				if err := s.stream.Send(frame); err != nil {
					s.Close()
					return
				}
			case <-s.done:
				return
			}
		}
	}()
	<-s.done
	return nil
}

func (s *AgentStream) Send(frame contracts.ConsoleFrame) bool {
	select {
	case <-s.done:
		return false
	case s.to <- frame:
		return true
	}
}

func (s *AgentStream) Frames() <-chan contracts.ConsoleFrame {
	return s.from
}

func (s *AgentStream) Recv() (contracts.ConsoleFrame, bool) {
	select {
	case <-s.done:
		return contracts.ConsoleFrame{}, false
	case frame := <-s.from:
		return frame, true
	}
}

func (s *AgentStream) Close() {
	s.once.Do(func() {
		close(s.done)
	})
}

func randomToken(size int) (string, error) {
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
