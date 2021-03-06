package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/go-kit/kit/log"
	"github.com/pkg/errors"
)

// Exec runs the given cmd in /bin/sh, saves its stdout/stderr streams to
// the specified files. It waits until the execution terminates.
//
// On error, an exit code may be returned if it is an exit code error.
// Given stdout and stderr will be closed upon returning.
func Exec(ctx *log.Context, cmd, workdir string, stdout, stderr io.WriteCloser, cfg *handlerSettings) (int, error) {
	defer stdout.Close()
	defer stderr.Close()

	commandArgs, err := SetEnvironmentVariables(cfg)
	cmd = cmd + commandArgs // Add command args if any

	//executionMessage := ""   // TODO: return
	exitCode := 0 // TODO: return exit code and execution state
	var command *exec.Cmd
	if cfg.publicSettings.TimeoutInSeconds > 0 {
		commandContext, cancel := context.WithTimeout(context.Background(), time.Duration(1)*time.Second)
		defer cancel()
		command = exec.CommandContext(commandContext, "/bin/bash", "-c", cmd)
		ctx.Log("message", "Execute with TimeoutInSeconds="+strconv.Itoa(cfg.publicSettings.TimeoutInSeconds))
	} else {
		command = exec.Command("/bin/bash", "-c", cmd)
	}

	// If RunAsUser is set by customer we need to execute the script under that user
	// Password is not needed because extension process runs under root and has permission to execute under different user
	if cfg.publicSettings.RunAsUser != "" {
		ctx.Log("message", "RunAsUser="+cfg.publicSettings.RunAsUser)
		runAsUser, err := user.Lookup(cfg.publicSettings.RunAsUser)
		if err != nil {
			return exitCode, err
		}

		uid, _ := strconv.Atoi(runAsUser.Uid)
		gid, _ := strconv.Atoi(runAsUser.Gid)

		command.SysProcAttr = &syscall.SysProcAttr{}
		command.SysProcAttr.Credential = &syscall.Credential{Uid: uint32(uid),
			Gid: uint32(gid)}
	}

	command.Dir = workdir
	command.Stdout = stdout
	command.Stderr = stderr
	err = command.Run()
	if err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if ok {
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
				exitCode = status.ExitStatus()
				if status.Signaled() { // Timed out
					ctx.Log("message", "Timeout:"+err.Error())
				}
				return exitCode, fmt.Errorf("command terminated with exit status=%d", exitCode)
			}
		}
	}

	return exitCode, errors.Wrapf(err, "failed to execute command")
}

func SetEnvironmentVariables(cfg *handlerSettings) (string, error) {
	var err error
	commandArgs := ""
	parameters := []parameterDefinition{}
	if cfg.publicSettings.Parameters != nil && len(cfg.publicSettings.Parameters) > 0 {
		parameters = cfg.publicSettings.Parameters
	}
	if cfg.protectedSettings.ProtectedParameters != nil && len(cfg.protectedSettings.ProtectedParameters) > 0 {
		parameters = append(parameters, cfg.protectedSettings.ProtectedParameters...)
	}

	for i := 0; i < len(parameters); i++ {
		name := parameters[i].Name
		value := parameters[i].Value
		if value != "" {
			if name != "" { // Named parameters are set as environmental setting
				err = os.Setenv(name, value)
			} else { // Unnamed parameters go to command args
				commandArgs += " " + value
			}
		}
	}

	return commandArgs, err // Return command args and the last error if any
}

// ExecCmdInDir executes the given command in given directory and saves output
// to ./stdout and ./stderr files (truncates files if exists, creates them if not
// with 0600/-rw------- permissions).
//
// Ideally, we execute commands only once per sequence number in run-command-handler,
// and save their output under /var/lib/waagent/<dir>/download/<seqnum>/*.
func ExecCmdInDir(ctx *log.Context, scriptFilePath, workdir string, cfg *handlerSettings) error {

	stdoutFileName, stderrFileName := logPaths(workdir)

	outF, err := os.OpenFile(stdoutFileName, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return errors.Wrapf(err, "failed to open stdout file")
	}
	errF, err := os.OpenFile(stderrFileName, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return errors.Wrapf(err, "failed to open stderr file")
	}

	_, err = Exec(ctx, scriptFilePath, workdir, outF, errF, cfg)

	return err
}

// logPaths returns stdout and stderr file paths for the specified output
// directory. It does not create the files.
func logPaths(dir string) (stdout string, stderr string) {
	return filepath.Join(dir, "stdout"), filepath.Join(dir, "stderr")
}
