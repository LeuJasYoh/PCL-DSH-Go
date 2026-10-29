// SPDX-License-Identifier: MIT

package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/apierr"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/app"
	"github.com/LeuJasYoh/PCL-DSH-Go/internal/server/job"
)

// TestLaunchPrecheckSharesRunningLimit verifies the API synchronous precheck
// uses app.MaxRunningInstances (production default 0 = unlimited; this fake
// starts at 1 to assert the 409 path) and returns the same
// message/code as app.LaunchInstance.
func TestLaunchPrecheckSharesRunningLimit(t *testing.T) {
	env := newTestEnv(t, false)
	sess := env.loginAs(t)
	instance := env.createTestInstance(t, "限额实例", freePort(t))

	env.app.mu.Lock()
	env.app.runtimes = []app.RuntimeInfo{{Version: "0.2.1-rc.3", Installed: true, Compatibility: "verified"}}
	env.app.running = []app.InstanceRuntimeStatus{{InstanceID: "i-111111111111", Status: "运行中", Ready: true}}
	env.app.mu.Unlock()

	// 默认 limit=1：已有一个其它实例运行 → 409 + 可操作消息。
	rec := env.request(t, http.MethodPost, "/api/instances/"+instance.ID+"/launch", nil, sess.withSession())
	body := decodeError(t, rec, http.StatusConflict, apierr.CodeConflictRunningInstance)
	if !strings.Contains(body.Message, "当前最多允许同时运行 1 个 DSH 实例") {
		t.Fatalf("错误信息未包含当前上限: %q", body.Message)
	}
	if !strings.Contains(body.Message, "--pcl.max-running-instances") {
		t.Fatalf("错误信息未提示配置项: %q", body.Message)
	}

	// limit=2：一个其它实例运行仍允许启动第二个。
	env.app.mu.Lock()
	env.app.maxRunning = 2
	env.app.mu.Unlock()
	rec = env.request(t, http.MethodPost, "/api/instances/"+instance.ID+"/launch", nil, sess.withSession())
	view := decodeJSONResponse[job.View](t, rec, http.StatusAccepted)
	env.waitForJobState(t, sess, view.ID, job.StateSucceeded)
	// 清掉 fakeApp 的 live 标记，避免下一次预检命中“本实例已在运行”。
	env.app.mu.Lock()
	delete(env.app.live, instance.ID)
	env.app.mu.Unlock()

	// limit=0：不限制（多个其它实例运行也允许）。
	env.app.mu.Lock()
	env.app.maxRunning = 0
	env.app.running = []app.InstanceRuntimeStatus{
		{InstanceID: "i-111111111111", Status: "运行中", Ready: true},
		{InstanceID: "i-222222222222", Status: "运行中", Ready: true},
		{InstanceID: "i-333333333333", Status: "运行中", Ready: true},
	}
	env.app.mu.Unlock()
	rec = env.request(t, http.MethodPost, "/api/instances/"+instance.ID+"/launch", nil, sess.withSession())
	view = decodeJSONResponse[job.View](t, rec, http.StatusAccepted)
	env.waitForJobState(t, sess, view.ID, job.StateSucceeded)
}
