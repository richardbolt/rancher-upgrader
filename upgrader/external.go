package upgrader

import (
	"bufio"
	"fmt"
	"log"
	"os/exec"
)

// StreamingExternalCmd takes a command string with a list of string args and runs the command.
// It streams the command output to stdout and stderr (to stderr) and returns an error if the command
// exits with a non-zero status code.
func StreamingExternalCmd(command string, args ...string) error {
	cmd := exec.Command(command, args...)
	cmdReader, err := cmd.StdoutPipe()
	if err != nil {
		log.Println("Error creating StdoutPipe for external command", err)
		return err
	}
	// Asyncify the output from the command and print it out.
	scanner := bufio.NewScanner(cmdReader)
	go func() {
		for scanner.Scan() {
			fmt.Printf(scanner.Text())
		}
	}()

	log.Println("Starting external command")
	err = cmd.Start()
	if err != nil {
		log.Println("Error with external command", err)
		return err
	}

	err = cmd.Wait()
	if err != nil {
		log.Println("Error waiting for external command", err)
		return err
	}
	return nil
}
