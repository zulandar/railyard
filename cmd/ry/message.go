package main

import (
	"fmt"
	"strconv"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/zulandar/railyard/internal/messaging"
)

func newMessageCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "message",
		Short: "Messaging commands",
	}

	cmd.AddCommand(newMessageSendCmd())
	cmd.AddCommand(newMessageAckCmd())
	cmd.AddCommand(newMessageThreadCmd())
	return cmd
}

func newMessageSendCmd() *cobra.Command {
	var (
		configPath string
		from       string
		to         string
		subject    string
		body       string
		carID     string
		threadID   uint
		priority   string
	)

	cmd := &cobra.Command{
		Use:   "send",
		Short: "Send a message to an agent",
		Long:  "Sends a message from one agent to another, with optional car and thread context.",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, gormDB, err := connectFromConfig(configPath)
			if err != nil {
				return err
			}

			opts := messaging.SendOpts{
				CarID:   carID,
				Priority: priority,
			}
			if cmd.Flags().Changed("thread-id") {
				opts.ThreadID = &threadID
			}

			msg, err := messaging.Send(gormDB, from, to, subject, body, opts)
			if err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Sent message %d to %s\n", msg.ID, to)
			return nil
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	cmd.Flags().StringVar(&from, "from", "", "sender agent ID (required)")
	cmd.Flags().StringVar(&to, "to", "", "recipient agent ID (required)")
	cmd.Flags().StringVar(&subject, "subject", "", "message subject (required)")
	cmd.Flags().StringVar(&body, "body", "", "message body (required)")
	cmd.Flags().StringVar(&carID, "car-id", "", "associated car ID")
	cmd.Flags().UintVar(&threadID, "thread-id", 0, "thread ID to attach to")
	cmd.Flags().StringVar(&priority, "priority", "normal", "message priority (normal, urgent)")
	cmd.MarkFlagRequired("from")
	cmd.MarkFlagRequired("to")
	cmd.MarkFlagRequired("subject")
	cmd.MarkFlagRequired("body")
	return cmd
}

func newInboxCmd() *cobra.Command {
	var (
		configPath string
		agent      string
	)

	cmd := &cobra.Command{
		Use:   "inbox",
		Short: "View an agent's inbox",
		Long:  "Lists unacknowledged messages for an agent, ordered by priority and creation time.",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, gormDB, err := connectFromConfig(configPath)
			if err != nil {
				return err
			}

			msgs, err := messaging.Inbox(gormDB, agent)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if len(msgs) == 0 {
				fmt.Fprintf(out, "No messages for %s\n", agent)
				return nil
			}

			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tFROM\tSUBJECT\tPRIORITY\tCREATED")
			for _, m := range msgs {
				fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\n",
					m.ID, m.FromAgent, m.Subject, m.Priority,
					m.CreatedAt.Format("2006-01-02 15:04"))
			}
			w.Flush()
			return nil
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	cmd.Flags().StringVar(&agent, "agent", "", "agent ID to check inbox (required)")
	cmd.MarkFlagRequired("agent")
	return cmd
}

func newMessageAckCmd() *cobra.Command {
	var (
		configPath string
		broadcast  bool
		agent      string
	)

	cmd := &cobra.Command{
		Use:   "ack <message-id>",
		Short: "Acknowledge a message",
		Long:  "Marks a message as acknowledged. Use --broadcast with --agent for broadcast messages.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseUint(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid message ID: %w", err)
			}

			_, gormDB, err := connectFromConfig(configPath)
			if err != nil {
				return err
			}

			if broadcast {
				if agent == "" {
					return fmt.Errorf("--agent is required when using --broadcast")
				}
				if err := messaging.AcknowledgeBroadcast(gormDB, uint(id), agent); err != nil {
					return err
				}
			} else {
				if err := messaging.Acknowledge(gormDB, uint(id)); err != nil {
					return err
				}
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Acknowledged message %d\n", id)
			return nil
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	cmd.Flags().BoolVar(&broadcast, "broadcast", false, "acknowledge a broadcast message")
	cmd.Flags().StringVar(&agent, "agent", "", "agent ID (required with --broadcast)")
	return cmd
}

func newMessageThreadCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "thread <thread-id>",
		Short: "View a message thread",
		Long:  "Displays all messages in a thread, ordered by creation time.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseUint(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid thread ID: %w", err)
			}

			_, gormDB, err := connectFromConfig(configPath)
			if err != nil {
				return err
			}

			msgs, err := messaging.GetThread(gormDB, uint(id))
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if len(msgs) == 0 {
				fmt.Fprintf(out, "No messages in thread %d\n", id)
				return nil
			}

			for _, m := range msgs {
				fmt.Fprintf(out, "[%s] %s → %s: %s\n%s\n\n",
					m.CreatedAt.Format("2006-01-02 15:04"),
					m.FromAgent, m.ToAgent, m.Subject, m.Body)
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	return cmd
}
