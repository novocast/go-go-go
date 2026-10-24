package track

import (
	"bufio"
	"io"
	"os"

	"golang.org/x/term"
)

type TerminalSession struct {
	stdin       *os.File
	stdout      *os.File
	state       *term.State
	interactive bool
	rawInput    bool
	entered     bool
}

func PrepareTerminal(stdin, stdout *os.File) (*TerminalSession, error) {
	session := &TerminalSession{stdin: stdin, stdout: stdout, interactive: term.IsTerminal(int(stdout.Fd()))}
	if session.interactive && term.IsTerminal(int(stdin.Fd())) {
		state, err := term.MakeRaw(int(stdin.Fd()))
		if err != nil {
			return nil, err
		}
		session.state = state
		session.rawInput = true
	}
	return session, nil
}

func (session *TerminalSession) Interactive() bool { return session.interactive }
func (session *TerminalSession) RawInput() bool    { return session.rawInput }

func (session *TerminalSession) Enter() error {
	if !session.interactive || session.entered {
		return nil
	}
	session.entered = true
	if _, err := io.WriteString(session.stdout, "\x1b[?1049h\x1b[?25l\x1b[2J\x1b[H"); err != nil {
		return err
	}
	return nil
}

func (session *TerminalSession) Size() (int, int) {
	if session.interactive {
		if width, height, err := term.GetSize(int(session.stdout.Fd())); err == nil {
			return width, height
		}
	}
	return 80, 24
}

func (session *TerminalSession) Restore() error {
	var outputErr error
	if session.entered {
		_, outputErr = io.WriteString(session.stdout, "\x1b[0m\x1b[?25h\x1b[?1049l")
		session.entered = false
	}
	if session.state != nil {
		if err := term.Restore(int(session.stdin.Fd()), session.state); outputErr == nil {
			outputErr = err
		}
		session.state = nil
	}
	return outputErr
}

type Key uint8

const (
	KeyUp Key = iota
	KeyDown
	KeyEnter
	KeyAny
	KeyInterrupt
	KeyQuit
)

func ReadKeys(input io.Reader, output chan<- Key) {
	defer close(output)
	reader := bufio.NewReader(input)
	for {
		value, err := reader.ReadByte()
		if err != nil {
			return
		}
		switch value {
		case 3:
			output <- KeyInterrupt
		case '\r', '\n':
			output <- KeyEnter
		case 'q', 'Q':
			output <- KeyQuit
		case 0x1b:
			marker, err := reader.ReadByte()
			if err != nil {
				return
			}
			if marker != '[' {
				continue
			}
			direction, err := reader.ReadByte()
			if err != nil {
				return
			}
			switch direction {
			case 'A':
				output <- KeyUp
			case 'B':
				output <- KeyDown
			}
		default:
			output <- KeyAny
		}
	}
}
