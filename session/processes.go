// Copyright (c) 2014-present, b3log.org
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package session

import (
	"bytes"
	"encoding/json"
	"github.com/88250/gulu"
	"github.com/88250/wide/conf"
	"github.com/88250/wide/util"
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
)

// Type of process set.
type procs map[string][]*os.Process

// Processse of all users.
//
// <sid, []*os.Process>
var Processes = procs{}

// Exclusive lock.
var procMutex sync.Mutex

// RunHandler handles request of executing a binary file.
func RunHandler(w http.ResponseWriter, r *http.Request, channel map[string]*util.WSChannel) {
	result := gulu.Ret.NewResult()
	defer gulu.Ret.RetResult(w, r, result)

	if conf.Wide.ReadOnly {
		result.Code = -1
		result.Msg = "readonly mode"
		return
	}

	var args map[string]interface{}

	if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
		logger.Error(err)
		result.Code = -1
	}

	sid := args["sid"].(string)
	wSession := WideSessions.Get(sid)
	if nil == wSession {
		result.Code = -1
	}

	filePath := args["executable"].(string)

	randInt := rand.Int()
	rid := strconv.Itoa(randInt)
	var cmd *exec.Cmd
	if conf.Docker {
		fileName := filepath.Base(filePath)
		cmd = exec.Command("docker", "run", "--rm", "--name", rid, "-v", filePath+":/"+fileName,
			"--memory", "64M", "--cpus", "0.1",
			conf.DockerImageGo, "/"+fileName)
	} else {
		logger.Debugf("run exec:[%s]", filePath)
		//cmd = exec.Command("sleep 0.1 && sh " + filePath)
		cmd = exec.Command("sh", "-c", filePath)
		curDir := filepath.Dir(filePath)
		cmd.Dir = curDir
	}

	var outBuf bytes.Buffer
	var errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	//outBuf := &bytes.Buffer{}
	//errBuf := &bytes.Buffer{}
	//cmd.Stdout = outBuf
	//cmd.Stderr = errBuf

	if err := cmd.Run(); nil != err {
		logger.Error(err)
		result.Code = -1
	}
	res := outBuf.String()

	logger.Debug("run cmd res %s ", res)
	wsChannel := channel[sid]
	logger.Debug("wschannel id %s ", sid)
	channelRet := map[string]interface{}{}
	channelRet["cmd"] = "run-done"
	channelRet["output"] = "<span class='build-succ'>" + res + "</span>\n"
	logger.Debug("run complete data &s ", &channelRet)
	wsChannel.WriteJSON(&channelRet)
	wsChannel.Refresh()

	channelRet["cmd"] = "run-complete"
	channelRet["output"] = "process run complete"
	logger.Debug("run complete data &s ", &channelRet)
	wsChannel.WriteJSON(&channelRet)
	wsChannel.Refresh()
}

// StopHandler handles request of stopping a running process.
func StopHandler(w http.ResponseWriter, r *http.Request) {
	result := gulu.Ret.NewResult()
	defer gulu.Ret.RetResult(w, r, result)

	var args map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
		logger.Error(err)
		result.Code = -1

		return
	}

	sid := args["sid"].(string)
	pid := int(args["pid"].(float64))

	wSession := WideSessions.Get(sid)
	if nil == wSession {
		result.Code = -1

		return
	}

	Processes.Kill(wSession, pid)
}

// Add adds the specified process to the user process set.
func (procs *procs) Add(wSession *WideSession, proc *os.Process) {
	procMutex.Lock()
	defer procMutex.Unlock()

	sid := wSession.ID
	userProcesses := (*procs)[sid]

	userProcesses = append(userProcesses, proc)
	(*procs)[sid] = userProcesses

	// bind process with wide session
	wSession.SetProcesses(userProcesses)

	logger.Tracef("Session [%s] has [%d] processes", sid, len((*procs)[sid]))
}

// Remove removes the specified process from the user process set.
func (procs *procs) Remove(wSession *WideSession, proc *os.Process) {
	procMutex.Lock()
	defer procMutex.Unlock()

	sid := wSession.ID

	userProcesses := (*procs)[sid]

	var newProcesses []*os.Process
	for i, p := range userProcesses {
		if p.Pid == proc.Pid {
			newProcesses = append(userProcesses[:i], userProcesses[i+1:]...) // remove it
			(*procs)[sid] = newProcesses

			// bind process with wide session
			wSession.SetProcesses(newProcesses)

			logger.Tracef("Session [%s] has [%d] processes", sid, len((*procs)[sid]))

			return
		}
	}
}

// Kill kills a process specified by the given pid.
func (procs *procs) Kill(wSession *WideSession, pid int) {
	procMutex.Lock()
	defer procMutex.Unlock()

	sid := wSession.ID

	userProcesses := (*procs)[sid]

	for i, p := range userProcesses {
		if p.Pid == pid {
			if err := p.Kill(); nil != err {
				logger.Errorf("Kill a process [pid=%d] of user [%s, %s] failed [error=%v]", pid, wSession.UserId, sid, err)
			} else {
				var newProcesses []*os.Process

				newProcesses = append(userProcesses[:i], userProcesses[i+1:]...)
				(*procs)[sid] = newProcesses

				// bind process with wide session
				wSession.SetProcesses(newProcesses)

				logger.Debugf("Killed a process [pid=%d] of user [%s, %s]", pid, wSession.UserId, sid)
			}

			return
		}
	}
}
