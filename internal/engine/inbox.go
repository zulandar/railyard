package engine

import (
	"fmt"
	"github.com/zulandar/railyard/internal/messaging"
	"github.com/zulandar/railyard/internal/models"
	"gorm.io/gorm"
)

// InstructionType classifies yardmaster instructions.
type InstructionType string

const (
	InstructionAbort       InstructionType = "abort"
	InstructionPause       InstructionType = "pause"
	InstructionResume      InstructionType = "resume"
	InstructionSwitchTrack InstructionType = "switch-track"
	InstructionGuidance    InstructionType = "guidance"
	InstructionUnknown     InstructionType = "unknown"
)

// Instruction represents a parsed yardmaster instruction from the inbox.
type Instruction struct {
	Type      InstructionType
	MessageID uint
	Subject   string
	Body      string
	CarID     string
}

// ClassifyMessage determines the instruction type from a message subject.
func ClassifyMessage(msg *models.Message) InstructionType {
	if msg == nil {
		return InstructionUnknown
	}
	switch msg.Subject {
	case "abort":
		return InstructionAbort
	case "pause":
		return InstructionPause
	case "resume":
		return InstructionResume
	case "switch-track":
		return InstructionSwitchTrack
	case "guidance":
		return InstructionGuidance
	default:
		return InstructionUnknown
	}
}

// ProcessInbox reads unacknowledged messages for an engine, classifies them
// as instructions, acknowledges each one, and returns the parsed instructions.
func ProcessInbox(db *gorm.DB, engineID string) ([]Instruction, error) {
	if engineID == "" {
		return nil, fmt.Errorf("engine: engineID is required")
	}

	msgs, err := messaging.Inbox(db, engineID)
	if err != nil {
		return nil, fmt.Errorf("engine: process inbox: %w", err)
	}

	var instructions []Instruction
	for i := range msgs {
		inst := Instruction{
			Type:      ClassifyMessage(&msgs[i]),
			MessageID: msgs[i].ID,
			Subject:   msgs[i].Subject,
			Body:      msgs[i].Body,
			CarID:     msgs[i].CarID,
		}
		instructions = append(instructions, inst)

		// Acknowledge the message.
		if msgs[i].ToAgent == "broadcast" {
			_ = messaging.AcknowledgeBroadcast(db, msgs[i].ID, engineID)
		} else {
			_ = messaging.Acknowledge(db, msgs[i].ID)
		}
	}

	return instructions, nil
}

// ShouldAbort checks if any instruction is an abort for the given car.
func ShouldAbort(instructions []Instruction, carID string) bool {
	for _, inst := range instructions {
		if inst.Type == InstructionAbort && (inst.CarID == carID || inst.CarID == "") {
			return true
		}
	}
	return false
}

// ShouldPause checks if any instruction requests a pause.
func ShouldPause(instructions []Instruction) bool {
	for _, inst := range instructions {
		if inst.Type == InstructionPause {
			return true
		}
	}
	return false
}

// HasResume checks if any instruction is a resume.
func HasResume(instructions []Instruction) bool {
	for _, inst := range instructions {
		if inst.Type == InstructionResume {
			return true
		}
	}
	return false
}
