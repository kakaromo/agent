# 05. REST / SSE / WebSocket API 레퍼런스

portal `AgentController` / `ScheduledJobController` / `JobExecutionController` / `AgentTraceArchiveController` 의 endpoint 를 standalone agent (Go) 에서 1:1 복제한 결과.

## 공통 규칙

- **Prefix**: `/api/agent` (모든 endpoint)
- **응답 Content-Type**: `application/json` (SSE 는 `text/event-stream`)
- **`serverId` 쿼리 파라미터**: 모든 endpoint 가 받지만 standalone 에서는 무시 (다중 서버 등록 호환용)
- **JSON 키**: camelCase (`deviceId`, `jobId`, `progressPercent`)
- **enum 값**: 소문자 문자열 (`completed`, `online`, `fio`)
- **JSON 직렬화**: `server/rest_convert.go` 의 `*ToMap` 함수들이 portal Spring `LinkedHashMap` 과 동일 결과
- **에러 응답**: `{"error": "메시지"}` + 적절한 HTTP status
- **404 + state body**: 일부 endpoint 는 만료 잡 시 `{"error":"...", "state":"failed"}` 로 응답. client.ts 가 정상 데이터로 처리

## 1. Device

### `GET /api/agent/devices?serverId=`
ADB 로 인식된 디바이스 목록.

**응답:**
```json
{
  "devices": [{
    "deviceId": "2-1.1.2",
    "serial": "R3CY10SD7RE",
    "state": "online",
    "androidVersion": "16",
    "model": "SM S938N",
    "board": "sun",
    "platform": "sun",
    "hardware": "qcom",
    "cpuAbi": "arm64-v8a",
    "buildId": "BP2A.250605.031.A3.S938NKSS9BZCH",
    "manufacturer": "samsung",
    "sdkVersion": 36
  }]
}
```

`state` 값: `online | offline | busy | unknown`

### `POST /api/agent/devices/{serial}/connect?serverId=`
ADB connect 호출 (TCP/IP 모드). USB 연결 디바이스에는 부적합.

응답: `{"success": bool, "message": "..."}`

### `POST /api/agent/devices/{serial}/disconnect?serverId=`
ADB disconnect. 응답: `{"success": bool}`

## 2. Server (AgentServer DB CRUD)

### `GET /api/agent/servers`
등록된 agent 서버 목록. standalone 부팅 시 localhost 가 자동 seed 됨.

응답:
```json
[{
  "id": 1,
  "name": "localhost (this agent:50051)",
  "host": "localhost",
  "port": 50051,
  "enabled": true,
  "description": "Auto-registered local standalone agent",
  "createdAt": "2026-05-18T05:51:42.071636Z",
  "updatedAt": "..."
}]
```

### `POST /api/agent/servers`
신규 서버 등록.

요청:
```json
{"name":"office-agent","host":"10.0.0.5","port":50051,"description":"office"}
```

응답: 생성된 row (위 shape).

### `PUT /api/agent/servers/{id}`
업데이트. 요청/응답 동일 shape.

### `DELETE /api/agent/servers/{id}`
삭제. 응답: `{"success": true}`

### `POST /api/agent/servers/test`
임의 host:port 도달 가능성 테스트 (등록 전).

요청: `{"host":"127.0.0.1","port":50051}`
응답:
```json
{"success":true,"host":"127.0.0.1","port":50051,"message":"연결 성공"}
```

### `POST /api/agent/servers/{id}/test`
등록된 서버 id 의 host:port 테스트. 응답 위와 동일.

### `GET /api/agent/servers/{id}/status`
gRPC connection state (standalone 에선 TCP reachable 만 확인).

응답:
```json
{"serverId":1,"state":"READY","connected":true,"host":"localhost","port":50051}
```

`state` 값: `READY | IDLE | TRANSIENT_FAILURE`

### `POST /api/agent/servers/{id}/reconnect`
재연결. standalone 에선 별도 connection pool 이 없으므로 TCP 재테스트만.

응답: `{"success":bool,"state":"...","message":"재연결 성공/실패"}`

## 3. Benchmark

### `POST /api/agent/benchmark/run?serverId=`
잡 시작 + JobExecution INSERT.

요청:
```json
{
  "deviceIds": ["2-1.1.2"],
  "tool": "FIO",
  "params": {
    "rw": "randread",
    "bs": "4k",
    "size": "32m",
    "runtime": "5"
  },
  "jobName": "smoke-test",
  "busyPolicy": "reject"
}
```

`tool` 값: `FIO | IOZONE | TIOTEST | IOTEST` (대문자, 또는 소문자/proto enum 모두 허용).
`busyPolicy`: `reject` (기본) | `wait` | `force`.

응답: `{"jobId":"uuid-..."}`

### `GET /api/agent/benchmark/status?serverId=&jobId=`
잡 상태 + 디바이스별 진행.

응답:
```json
{
  "jobId":"...",
  "state":"running",
  "totalDevices":1,
  "completedDevices":0,
  "failedDevices":0,
  "deviceStatuses":[{
    "deviceId":"2-1.1.2",
    "state":"running",
    "message":"running fio",
    "progressPercent":20
  }]
}
```

`state` 값: `queued | pushing_tools | running | collecting | completed | failed | partially_failed | cancelled | reparsing | unknown`

**hook**: terminal state 도달 시 자동으로 DB `state`/`completed_at`/`result_summary` 동기화.
**404 + body**: 잡 만료 시 `{"error":"Job not found: ...","state":"failed"}` 반환.

### `GET /api/agent/benchmark/result?serverId=&jobId=&deviceId=`
완료된 잡의 metrics.

응답:
```json
{
  "results":[{
    "deviceId":"2-1.1.2",
    "tool":"fio",
    "rawOutput":"{ ... fio JSON ... }",
    "metrics":{
      "read_iops":417483,
      "read_bw_kb":1669932,
      "read_clat_ns_mean":1453.6,
      "read_clat_ns_p99.000000":2704,
      "read_clat_ns_p99.900000":64768,
      ...
    },
    "startedAt":1779051874055,
    "finishedAt":1779051877205,
    "success":true,
    "error":""
  }]
}
```

scenario 시나리오에서 trace_start step 이 있었으면 `traceJobs: [{traceJobId, stepIndex, ...}]` 도 포함.

**404 + body**: 잡 만료 시 `{"error":"Job not found: ...","state":"failed","results":[]}`.

### SSE `GET /api/agent/benchmark/progress?serverId=&jobId=`

portal `EventSource` 호환 진행률 스트림.

응답 (event-stream):
```
event: progress
data: {"jobId":"...","deviceId":"2-1.1.2","state":"pushing_tools","message":"pushing fio","progressPercent":10,"error":""}

event: progress
data: {"jobId":"...","deviceId":"2-1.1.2","state":"running","message":"running fio","progressPercent":20,"error":""}

event: progress
data: {...,"state":"completed","progressPercent":100,"metrics":{...},"rawOutput":"..."}

event: complete
data: {}
```

명명 이벤트:
- `progress` — 진행률 업데이트 (terminal state 도 progress 로 한 번 더 옴)
- `complete` — 잡 종료 시그널, 이후 SSE close
- `error` — 채널 에러

30초마다 `: keepalive <ts>` comment 라인 (idle 끊김 방지).

**hook**: terminal state 또는 complete 시 OnState + OnResult 호출.

## 4. Job 관리

### `DELETE /api/agent/jobs/{jobId}?serverId=`
잡 삭제 (orchestrator 메모리 + trace.Manager 양쪽 시도). 응답 `{"success":bool,"message":"..."}`

### `POST /api/agent/jobs/{jobId}/cancel?serverId=`
실행 중 잡 cancel. 응답 동일 shape.

## 5. Trace

### `POST /api/agent/trace/start?serverId=`
ftrace 활성화 + trace_pipe 캡처 시작.

요청:
```json
{
  "deviceId": "2-1.1.2",
  "traceType": "ufs",
  "windowSeconds": 1,
  "jobName": "trace-test"
}
```

`traceType`: `ufs | block | both`

응답: `{"jobId":"..."}`

### `POST /api/agent/trace/{jobId}/stop?serverId=`
ftrace 중지 + adb 종료 + COLLECTING 상태로 전이 후 즉시 리턴. parquet 생성은 백그라운드.

응답: `{"success":bool,"message":"trace stopped"}`

### `POST /api/agent/trace/{jobId}/reparse?serverId=`
보존된 trace.log 를 다시 parquet 으로 파싱. 응답 동일 shape.

### `POST /api/agent/trace/result?serverId=`
완료된 trace 잡의 통계 (DuckDB parquet 집계).

요청:
```json
{
  "jobIds": ["uuid-..."],
  "filter": {
    "startTime": 0,
    "endTime": 100,
    "minDtoc": 0,
    "maxDtoc": 10,
    "cpuList": [0, 1],
    "cmdList": ["0x28"],
    "actionList": ["send_req"]
  },
  "latencyRangesMs": [0.1, 0.5, 1, 5, 10]
}
```

응답:
```json
{
  "jobId": "...",
  "stats": {
    "totalEvents": 8212,
    "durationSeconds": 0.001,
    "dtoc": {"min":..., "max":..., "avg":..., "median":..., "p99":..., "p999":..., "p9999":..., "p99999":..., "p999999":...},
    "ctod": { ... },
    "ctoc": { ... },
    "qd":   { ... },
    "cmdStats": [{"cmd":"0x28","count":8198,"ratio":99.83,...}],
    "latencyHistograms": [{"cmd":"0x28","latencyType":"dtoc","buckets":[{"rangeStartMs":...,"rangeEndMs":...,"count":...}]}],
    "cmdSizeCounts": [{"cmd":"0x28","size":4096,"count":1234}],
    "continuousCount": ..., "continuousRatio": ...,
    "alignedCount":    ..., "alignedRatio":    ...,
    "readTotalBytes":  ..., "writeTotalBytes": ..., "discardTotalBytes": ...,
    "sendCount":       ...,
    "directionContiguity": [
      {"direction":"read","contiguous":true,"count":1796,
       "ratioWithinDirection":72.2,"ratioOfSends":44.9,
       "totalBytes":120750080,"avgRequestBytes":67232.8}
    ],
    "classifiedSendCount": 4000,
    "addressRange": [
      {"direction":"all","minAddr":0,"maxAddr":122138624,"span":122138624,
       "count":1220317,"unitBytes":4096},
      {"direction":"read","minAddr":1048576,"maxAddr":122138624,"span":121090048,
       "count":418204,"unitBytes":4096}
    ]
  }
}
```

진행/수집 중 (RUNNING/COLLECTING/REPARSING) 잡은 명시적 에러 반환.

### `POST /api/agent/trace/raw?serverId=`
샘플링된 raw events (대용량 시 50만 이벤트 초과면 자동 샘플링).

요청: 위와 동일 (filter, jobIds).

응답:
```json
{
  "jobId":"...",
  "totalEvents": 1500000,
  "sampledEvents": 500000,
  "isSampled": true,
  "events":[{
    "time": 172953.7687,
    "lba": 6359183,
    "qd": 1,
    "cpu": 0,
    "dtoc": 0.05,
    "ctod": 0,
    "ctoc": 0,
    "cmd": "0x28",
    "size": 4096,
    "continuous": true,
    "action": "send_req"
  }, ...]
}
```

## 6. Scenario

### `POST /api/agent/scenario/run?serverId=`
multi-step 시나리오 실행. proto `ScenarioStep` / `ScenarioLoop` 구조 그대로 받는다.

요청 (단순화 예):
```json
{
  "deviceIds":["2-1.1.2"],
  "scenarioName":"warmup-then-fio",
  "steps":[
    {"type":"shell","cmd":"sync"},
    {"type":"benchmark","tool":"BENCHMARK_TOOL_FIO","params":{...}},
    {"type":"trace_start","traceType":"ufs"},
    {"type":"benchmark","tool":"BENCHMARK_TOOL_FIO","params":{...}},
    {"type":"trace_stop"}
  ],
  "loops":[{"startStep":1,"endStep":4,"count":3}]
}
```

응답: `{"jobId":"..."}`

이후 progress 는 benchmark 와 동일한 SSE 사용.

## 7. Monitoring

### SSE `GET /api/agent/monitoring/stream?serverId=&deviceIds=A&deviceIds=B&interval=1`

여러 디바이스 메트릭 동시 스트리밍.

응답 (event-stream):
```
event: metrics
data: {
  "deviceId":"2-1.1.2",
  "timestamp":1779054312663,
  "cpu":{"usagePercent":2.5,"perCorePercent":[8.2,4.0,0,0,2.0,4.0,0,0]},
  "memory":{"totalKb":11381316,"availableKb":3679096,"usedKb":7702220,"usagePercent":67.67},
  "disk":{"readBytes":342192656384,"writeBytes":260260220928,"readIos":10931957,"writeIos":12104833},
  "dataPartition":{"mountPoint":"/data","filesystem":"f2fs","totalBytes":...,"usedBytes":...,"availableBytes":...,"usagePercent":4.04}
}

event: metrics
data: {...}
```

`deviceIds` 미지정 시 online 디바이스 자동 선택.

## 8. Macro

### DB CRUD (6 endpoint)

| 메서드 | 경로 | 설명 |
|---|---|---|
| GET | `/api/agent/app-macros` | 매크로 목록 |
| GET | `/api/agent/app-macros/{id}` | 단건 |
| POST | `/api/agent/app-macros` | 생성 |
| PUT | `/api/agent/app-macros/{id}` | 수정 |
| DELETE | `/api/agent/app-macros/{id}` | 삭제 |
| POST | `/api/agent/app-macros/{id}/duplicate` | 복제 (`(copy)` 접미) |

응답 shape (생성 예):
```json
{
  "id":1,
  "name":"tap-test",
  "description":"...",
  "packageName":"com.example",
  "eventsJson":"[{\"t\":100,\"type\":\"tap\",\"x\":500,\"y\":1000}]",
  "deviceWidth":1080,
  "deviceHeight":2400,
  "createdAt":"...",
  "updatedAt":"..."
}
```

`eventsJson` 은 string 으로 저장. 클라이언트가 `JSON.parse(eventsJson)` 으로 풀어쓴다.

### gRPC 위임 (6 endpoint)

| 메서드 | 경로 | gRPC RPC |
|---|---|---|
| GET | `/api/agent/macro/installed-apps?serverId=&deviceId=` | ListInstalledApps |
| POST | `/api/agent/macro/start-recording` | StartRecording |
| POST | `/api/agent/macro/stop-recording` | StopRecording |
| POST | `/api/agent/macro/replay` | ReplayMacro |
| POST | `/api/agent/macro/screenshot` | TakeScreenshot |
| POST | `/api/agent/macro/ocr` | ScreenshotOcr |

**start-recording 요청**: `{"deviceId":"..."}`
**stop-recording 요청**: `{"deviceId":"...","sessionId":"..."}`
**stop-recording 응답**: `{"success":bool,"deviceWidth":int,"deviceHeight":int,"events":[{t,type,x,y,...}]}`

**replay 요청**: `{"deviceId":"...","events":[...],"sourceWidth":1080,"sourceHeight":2400,"jobId":""}`
**replay 응답**: `{"success":bool,"message":"...","ocrResults":{name:text},"metrics":{key:number}}`

**screenshot 요청**: `{"deviceId":"..."}`
**screenshot 응답**: `{"success":bool,"width":int,"height":int,"imageBase64":"<base64 PNG>"}`

**ocr 요청**: `{"deviceId":"...","extractPattern":"regex","region":{"x":0,"y":0,"width":1080,"height":200}}`
**ocr 응답**: `{"success":bool,"fullText":"...","extractedValue":"...","imageBase64":"..."}`

## 9. APK 관리 (3 endpoint)

호스트의 `tools/apks/` 폴더에 둔 `.apk` 파일을 디바이스에 설치/제거. DB 의존성 없이 사무실 / standalone 모두 활성화. 자세한 폴더 정책은 [`tools/apks/README.md`](../tools/apks/README.md).

| 메서드 | 경로 | gRPC RPC |
|---|---|---|
| GET | `/api/agent/apks` | ListBundledApks — 폴더 스캔 |
| POST | `/api/agent/apks/install` | InstallApk — `adb install -r` |
| POST | `/api/agent/apks/uninstall` | UninstallApk — `adb uninstall` |

**GET /api/agent/apks** 응답:
```json
[
  {"filename":"com.antutu.ABenchMark.apk","sizeBytes":104857600,"modifiedAt":"2026-05-20T07:00:00Z"}
]
```

**install 요청**: `{"deviceId":"...","apkFilename":"foo.apk","grantPermissions":false}`
- `apkFilename` 은 `tools/apks/` 안의 bare 파일명. `/`, `\`, `..` 거부.
- `grantPermissions=true` 면 `pm install -g` (런타임 권한 자동 부여).
- 서버는 항상 `-r` (reinstall) 로 설치.

**install 응답**: `{"success":bool,"message":"...","packageName":"..."}`
- `message` 에 adb stdout/stderr 가 들어감 (`Failure [INSTALL_PARSE_FAILED_NO_CERTIFICATES]` 등).
- `packageName` 은 파일명에서 best-effort 추출 (`com.foo.bar.apk` → `com.foo.bar`). 추출 실패 시 빈 문자열.

**uninstall 요청**: `{"deviceId":"...","packageName":"com.example","keepData":false}`
- `keepData=true` 면 `pm uninstall -k` (사용자 데이터/캐시 보존).

**uninstall 응답**: `{"success":bool,"message":"..."}`

scenario step 으로도 사용 가능 — [`08-benchmark-scenario.md`](08-benchmark-scenario.md#step-type) 참고.

## 10. Preset / Template (13 endpoint)

### BenchmarkPreset (4)
`GET / POST /api/agent/benchmark-presets`, `PUT / DELETE /api/agent/benchmark-presets/{id}`

응답:
```json
{
  "id":1,
  "name":"fio-randread",
  "description":"4k random read",
  "tool":"FIO",
  "paramsJson":"{\"rw\":\"randread\",\"bs\":\"4k\"}",
  "createdAt":"...",
  "updatedAt":"..."
}
```

### IOTestPreset (4)
`GET / POST /api/agent/iotest-presets`, `PUT / DELETE /api/agent/iotest-presets/{id}`

응답: `BenchmarkPreset` 과 비슷하나 `tool` 대신 `category` (`Basic I/O`, `Random/Stress` 등), `configJson` (thread 배열 JSON).

### ScenarioTemplate (5)
`GET / POST /api/agent/scenario-templates`, `PUT / DELETE /api/agent/scenario-templates/{id}`, `POST /api/agent/scenario-templates/{id}/duplicate`

응답:
```json
{
  "id":1,
  "name":"warmup-then-fio",
  "description":"...",
  "repeatCount":3,
  "stepsJson":"[{...},{...}]",
  "loopsJson":"[{...}]",
  "createdAt":"...",
  "updatedAt":"..."
}
```

## 11. Schedule (cron, 7 endpoint)

| 메서드 | 경로 | 설명 |
|---|---|---|
| GET | `/api/agent/schedules` | 목록 |
| GET | `/api/agent/schedules/{id}` | 단건 |
| POST | `/api/agent/schedules` | 생성 + Reload |
| PUT | `/api/agent/schedules/{id}` | 수정 + Reload |
| DELETE | `/api/agent/schedules/{id}` | 삭제 + Reload |
| POST | `/api/agent/schedules/{id}/trigger` | 수동 실행 (cron 안 기다림) |
| POST | `/api/agent/schedules/{id}/enable` | enabled 토글 |

요청 (POST):
```json
{
  "name":"every-minute-smoke",
  "description":"...",
  "enabled":true,
  "type":"benchmark",
  "serverId":1,
  "deviceIds":"[\"2-1.1.2\"]",
  "config":"{\"tool\":\"FIO\",\"params\":{\"rw\":\"randread\",\"bs\":\"4k\",\"size\":\"16m\",\"runtime\":\"2\"}}",
  "cronExpression":"*/1 * * * *",
  "busyPolicy":"reject",
  "retryCount":0,
  "retryDelaySeconds":60,
  "notifyOnFailure":false,
  "notifyOnSuccess":false,
  "notifyWebhookUrl":null
}
```

`deviceIds`, `config` 는 JSON 문자열 (DB에 그대로 저장).
`cronExpression`: 표준 5-field (분 시 일 월 요일) 또는 robfig descriptor (`@every 5m` 등) 지원.
`type`: `benchmark | scenario` (scenario dispatch 는 Phase 7 후속).

응답:
```json
{
  "id":1,
  ...(요청 필드들)...,
  "lastRunAt": null,
  "lastRunStatus": null,
  "nextRunAt": "2026-05-18T07:13:00Z",
  "createdAt":"...",
  "updatedAt":"..."
}
```

trigger 응답: `{"success":true,"jobId":"new-uuid"}`

## 12. Execution history (5 endpoint)

### `GET /api/agent/executions?serverId=&type=&state=&page=&size=`

Spring Page<T> 호환 응답.

쿼리 파라미터:
- `serverId` (선택)
- `type`: `benchmark | scenario | trace`
- `state`: `running | completed | failed | ...`
- `page` (default 0)
- `size` (default 30, max 500)

응답:
```json
{
  "content": [{
    "id":1,
    "jobId":"...",
    "serverId":1,
    "serverName":"localhost (this agent:50051)",
    "type":"benchmark",
    "tool":"FIO",
    "jobName":"phase8-test",
    "deviceIds":"[\"2-1.1.2\"]",
    "state":"completed",
    "config":"{...}",
    "resultSummary":"{...summary JSON...}",
    "scheduledJobId":null,
    "retryAttempt":0,
    "errorMessage":null,
    "startedAt":"...",
    "completedAt":"...",
    "createdAt":"...",
    "traceRawKey":null,
    "traceRawFormat":null,
    "traceRawSize":null,
    "traceRawUploadedAt":null,
    "traceParquetKeys":null,
    "traceParsedAt":null,
    "traceParseState":null,
    "traceParseError":null
  }],
  "totalElements": 1,
  "totalPages": 1,
  "page": 0,
  "size": 30
}
```

### `GET /api/agent/executions/{id}`
단건 조회.

### `GET /api/agent/executions/by-job-id/{jobId}`
jobId 로 조회.

### `DELETE /api/agent/executions/{id}`
삭제. `{"success":true}`

### `GET /api/agent/executions/stats?serverId=`
요약 통계.

응답:
```json
{
  "total":18,
  "completed":1,
  "failed":17,
  "running":0,
  "successRate":0.0556
}
```

## 13. Archive (2 endpoint, 로컬 디스크 fallback)

### `POST /api/agent/upload/trace?serverId=`
trace jobIds 의 parquet + trace.log 를 archive_base 로 복사.

요청: `{"jobIds":["..."],"remotePath":"my-run"}`
응답:
```json
{
  "success":true,
  "message":"archived 2 files to /Users/.../archive",
  "uploadedFiles":[
    "/Users/.../archive/my-run/{jobId}/result_ufs.parquet",
    "/Users/.../archive/my-run/{jobId}/trace.log"
  ]
}
```

### `POST /api/agent/upload/benchmark?serverId=`
잡의 result JSON 을 archive_base 로 복사.

요청: `{"jobId":"...","remotePath":"my-run"}`
응답: `{"success":true,"message":"...","uploadedFiles":["/.../{deviceId}_result.json"]}`

## 14. Screen WebSocket

### `WS /api/agent/screen/{deviceId}?serverId=`

scrcpy H.264 video frame + control 메시지.

- 서버 → 클라: binary frame (H.264 NAL units) + 초기 JSON info (`{type:"info",device,serial,width,height,name}`)
- 클라 → 서버: JSON control 메시지 (`{type:"touch"|"key"|"scroll"|"back"|"requestSync"}`)
- portal frontend 의 JMuxer 가 video frame 을 디코드해 `<video>` 에 feed

**legacy path**: `WS /ws/screen/{deviceId}` 도 동일하게 동작 (호환).

## 15. 보조 WebSocket (portal 미사용)

portal 은 SSE 만 쓰지만, 다른 클라이언트 호환용으로 유지.

### `WS /ws/jobs/{jobId}/progress`
SSE 와 동일한 progress 데이터를 WebSocket message 로.

### `WS /ws/monitor?serials=A,B&interval_seconds=1`
SSE monitoring 과 동일.

## 16. on-device AI 측정 — Logcat / trace_marker (9 endpoint)

on-device AI(LLM) 의 TTFT/TPOT 를 logcat 문구에서 뽑는다. 런타임이 찍는 문자열이
AP·세트·버전마다 달라 정규식을 **DB 프리셋**으로 두고, 형식을 모를 땐 탐색으로 찾는다.

⚠ **수집 시작/중지 endpoint 는 없다.** logcat 은 잡 옵션으로 켜지므로(아래) 시나리오
실행 경로가 수집을 관장한다. 여기 REST 는 **이미 수집된 로그를 읽는 쪽**이다.

⚠ standalone 전용이 아니다 — 탐색/파싱은 DB 없이도 동작하도록 DB 블록 밖에 등록한다
(사무실 모드에서 로그 형식 조사). `profileId` 로 파싱할 때만 DB 가 필요하다.

### 잡에서 수집 켜기 (REST 아님, 시나리오 파라미터)

```
logcat=on            수집 켜기
logcat_tags=A,B,C    measure 모드 (해당 태그만 — 실측정엔 필수)
(태그 없음)           explore 모드 (전체 버퍼, 탐색용 1회성)
```

⚠ explore 는 전체 버퍼를 받으므로 그 자체가 IO/CPU 를 써서 수백 ms 단위 TTFT 를
흔든다. 실측정에는 반드시 태그를 지정한다.

⚠ 잡 수집은 **epoch 축**(`logcat -v epoch`)으로 고정이다. IO 트레이스가 전부
BOOTTIME 인데 `-v monotonic` 은 누적 suspend 만큼 어긋난다(실기기 실측 120.5초).

### `POST /api/agent/logcat/explore`

태그를 모를 때 후보를 찾는다. 결과는 **후보 제시까지**이고 프로파일을 자동 생성하지
않는다 — 사람이 원문을 보고 고르는 것이 오탐을 막는 유일한 방법이다.

요청:
```json
{ "jobId": "abc-123", "idleFrom": 100.0, "idleTo": 130.0, "runFrom": 140.0, "runTo": 160.0 }
```
`jobId` 대신 `path` 직접 지정도 가능 (허용 루트 밖 경로는 400 — 경로 격리 가드).
유휴/추론 구간을 주면 **차분**으로 "추론 때만 나타난 태그" 를 가려낸다. 벤더 이름을
몰라도 걸리는 방법이라 형식이 블랙박스일 때 사실상 유일한 수단이다.

응답:
```json
{
  "path": "/…/logcat.log",
  "result": {
    "totalLines": 282149, "parsedLines": 282136, "distinctTags": 3231,
    "candidates": [
      { "tag": "Genie", "pids": [900], "lines": 412,
        "unitHits": 88, "keywordHits": 120, "strongHits": 64,
        "onlyDuringRun": true, "score": 2680,
        "samples": ["101.500 900 900 I Genie: first token emitted — TTFT 2840 ms"] }
    ],
    "weakOnly": false,
    "diagnosis": []
  }
}
```

⚠⚠ **`weakOnly`** — 후보는 있으나 LLM 고유 신호(토큰 개념·prefill/decode·TTFT)가
**0건**이라는 뜻이다. 온디바이스 ML 은 종류를 불문하고 "모델 로드"·"추론" 이라는 같은
어휘를 쓰므로(음성 wakeword·얼굴인식 등) 목록이 전부 무관한 것일 수 있다. 화면은
이 경우를 반드시 눈에 띄게 구분해야 한다 — 목록이 있다는 것만으로 답이 있다고 읽히면
사용자가 헛수고한다.

### `POST /api/agent/logcat/parse`

저장된 패턴으로 TTFT/TPOT 를 뽑는다.

요청: `{ "jobId": "abc-123", "profileId": 3 }` 또는 `{ "path": "…", "patternsJson": "{…}" }`

⚠ **매칭 0건이어도 200 이다.** 에러로 만들면 "왜 0건인지" 진단이 화면까지 못 간다 —
그게 이 기능의 핵심 산출물이다(런타임이 stderr 로 뱉어 애초에 못 잡는 경우인지,
패턴이 틀린 것인지를 갈라준다). 실패 판정은 호출자가 `totalHits`/`partial` 로 한다.
반면 **패턴 자체가 잘못된 것은 400** 이다.

응답:
```json
{
  "path": "/…/logcat.log",
  "result": {
    "totalLines": 4210, "parsedLines": 4210, "matchedTags": ["Genie"],
    "marks": [ { "key": "prefill_begin", "timeSec": 1756272146.408 } ],
    "series": {
      "tpot": { "key": "tpot", "unit": "ms", "count": 128,
                "min": 22.1, "max": 41.7, "mean": 25.3, "median": 24.1, "p99": 38.9,
                "points": [ { "timeSec": 1756272146.5, "value": 24.1 } ] }
    },
    "stats": [ { "key": "tpot", "kind": "series", "hits": 128, "parseFailures": 0 } ],
    "totalHits": 129, "partial": false, "missingKeys": [], "diagnosis": []
  }
}
```

⚠ **`partial`** — 패턴 일부만 맞았다. 성공으로 처리하면 화면에 **반쪽 지표가 정상처럼**
뜬다(TTFT 는 나오는데 TPOT 은 없는 식). `parseFailures` 는 "정규식은 맞았는데 캡처
값이 숫자가 아니었다" = 패턴이 아니라 **캡처 그룹** 문제라 안내가 갈려야 한다.

series 요약에 median/p99 를 함께 주는 이유: TPOT 은 평균만 보면 "뒤로 갈수록 느려짐" 을
놓친다.

### `POST /api/agent/marker/explore`

**두 번째 소스 — ftrace `trace_marker`.** logcat 과 계약이 같고 소스만 다르다.

⚠ **왜 두 경로가 필요한가.** 런타임이 **stderr 로 뱉으면 logcat 에 아예 안 남는다**
(llama.cpp 의 `llama_print_timings()` 가 그렇다). 패턴을 아무리 고쳐도 소용없다.
trace_marker 는 파일 write 라 그 제약이 없고, IO 트레이스와 **같은 버퍼·같은
clock(boot)** 이라 축 변환도 필요 없다 (logcat 은 epoch → BOOTTIME 변환이 필요).

요청: `{ "traceJobId": "abc-123", "idleFrom": 100.0, "idleTo": 130.0, "runFrom": 140.0, "runTo": 160.0 }`

⚠ **`path` 를 받지 않는다.** logcat 쪽은 path + 격리 가드였지만 여기는 `traceJobId`
로 잡 폴더에서 파일명을 조립한다 — 임의 경로가 들어올 여지 자체를 없애는 쪽이
가드를 얹는 것보다 안전하다 (사무실 모드는 인증 없는 0.0.0.0 위다).

응답:
```json
{
  "path": "/…/trace.log",
  "result": {
    "totalLines": 44787, "markerLines": 574, "distinctNames": 210,
    "candidates": [
      { "name": "decode_ms_per_token", "kind": "counter", "count": 40,
        "hasValue": true, "min": 22, "max": 28,
        "llmSignal": true, "onlyDuringRun": true, "score": 10540,
        "samples": ["… C|9001|decode_ms_per_token|24"] }
    ],
    "weakOnly": false, "diagnosis": []
  }
}
```

**Android atrace 표준 포맷** (실기기 S25 실측):

```
B|pid|name           구간 시작
E|pid                구간 끝
C|pid|name|value     카운터 — 값이 이미 분리돼 있다
I|pid|name           순간 이벤트
```

⭐ **`C|` 가 logcat 대비 핵심 이점**: logcat 은 자유 문구라 사용자가 정규식과
**캡처 그룹**을 만들어야 하는데, `C|` 는 이름과 값이 파이프로 나뉘어 있어 **캡처
그룹 자체가 필요 없다.** 사용자는 이름만 적는다 (버전차가 있으면 regex).

⚠⚠ **`token` 단독은 신호로 못 쓴다.** 실기기에서 Android 시스템 마커가 상위를
통째로 먹었다 — Binder 가 이름 끝에 핸들을 붙인다:

```
B|2396|setTransactionState: transaction(Id:…)-token:0xb40000796fe7eea0
B|2992|serviceBind: BindServiceData{token=…}
```

Binder 토큰과 LLM 토큰은 이름만 같은 **완전히 다른 개념**이다 (logcat 에서 음성
wakeword 가 1위였던 것과 같은 실패 — 어휘가 겹친다). 그래서 토큰은 **LLM 문맥이
붙은 형태**만 본다: `tok/s`, `ms/tok`, `n_tokens`, `tokens_per_sec`.

### `POST /api/agent/marker/parse`

요청: `{ "traceJobId": "abc-123", "profileId": 3 }` 또는 `{ "traceJobId": "…", "patternsJson": {…} }`

패턴 구조가 logcat 과 다르다 — **캡처 그룹이 없다**:

```json
{
  "counters": [
    { "key": "ttft", "name": "llm.ttft_ms", "unit": "ms" },
    { "key": "tpot", "name": "decode_ms_per_token", "unit": "ms" },
    { "key": "heap", "regex": "^Heap size", "unit": "KB" }
  ],
  "sections": [ { "key": "prefill", "name": "prefill" } ]
}
```

`name` 은 정확 일치, `regex` 는 부분 일치 (이름이 버전마다 다를 때). 둘 다 비면 400.

응답 shape 은 **logcat parse 와 동일**하다 (`LogcatParseResult`) — 지표 성격이 같아
화면·진단·요약 로직을 그대로 재사용한다. 0건도 200 인 것도 같다.

### AILogProfile CRUD (5)

`GET / POST /api/agent/ai-log-profiles`, `GET / PUT / DELETE /api/agent/ai-log-profiles/{id}`

⚠ **`source` 필드로 소스를 구분한다** — `"logcat"`(기본) 또는 `"marker"`.
두 소스는 patterns_json 의 **필드 이름이 다르다**(`marks/series` vs
`counters/sections`). 섞으면 JSON 파싱은 통과하는데 매칭이 **조용히 0건**이 되고,
사용자는 정규식을 고쳐가며 헛수고한다. 그래서 저장 시 소스별 검증기를 타고,
파싱 전에 **양방향으로** 막는다 (틀리면 400 + 원인).

`GET` 은 `?runtime=` `?soc=` 필터를 받는다 — 기기가 붙었을 때 "이 AP 에 맞는 프로파일" 을
고르기 위해 컬럼으로 뺐다. ⚠ `soc` 필터는 **빈 soc 프로파일도 포함**한다 (빈 값 =
"런타임 공용" 이라 특정 soc 를 물었을 때 배제하면 공용 프로파일을 못 쓴다).

`patternsJson` 은 문자열/객체 양쪽을 받는다 (UI 는 객체가, 스크립트는 문자열이 자연스럽다).

```json
{ "name": "QNN Genie", "runtime": "qnn", "soc": "SM8650",
  "patternsJson": {
    "tags": ["Genie", "QnnHtp"],
    "marks":  [ { "key": "prefill_begin", "regex": "prefill begin" } ],
    "series": [ { "key": "ttft", "regex": "TTFT ([0-9.]+) ms", "unit": "ms" },
                { "key": "tpot", "regex": "decode ([0-9.]+) ms/tok", "unit": "ms" } ]
  } }
```

⚠ 저장 시점에 검증하고 실패는 **400** 이다 (500 을 주면 서버 탓처럼 보여 사용자가 자기
패턴을 고칠 생각을 못 한다). 막는 것들은 전부 "통과시키면 측정 시점에 조용히 틀리는" 종류다:
잘못된 정규식 / series 에 캡처 그룹 없음 / key 중복 / 패턴 0개.

⚠ mark 정규식은 **좁게** 쓴다. `stage=prefill` 처럼 시작(`stage=prefill tokens=384`)과
종료(`stage=prefill finished`) 양쪽에 걸리면 mark 가 2번 찍혀 구간 경계가 중복되고,
나중에 IO 를 겹칠 때 구간이 잘못 잘린다.

## HTTP status code 정리

| 상태 | 의미 |
|---|---|
| 200 | 정상 |
| 204 | (사용 안 함, 일부 헤더 처리에서만) |
| 400 | 요청 형식 오류 (body 파싱 실패, 필수 필드 누락) |
| 404 | 잡 만료 or 리소스 없음. **state body 동반** 시 client.ts 가 정상 데이터로 처리 |
| 405 | 메서드 미허용 |
| 500 | 서버 내부 오류 (proto/sqlite 등 예상치 못한 에러) |
| 503 | (Schedule trigger 시 runner 없음 등 특수 케이스) |

## client.ts 의 404 처리 (standalone-specific)

```ts
if (res.status === 404) {
  const errorText = await res.text().catch(() => '');
  try {
    const body = JSON.parse(errorText);
    if (body && body.state) return body as T;   // {state:"failed"} 등은 정상 데이터로
  } catch { /* not a state response */ }
  throw new Error('Not found');
}
```

이게 있어서 만료된 잡을 polling 해도 사용자에게 "API Error" 가 튀지 않음.

## endpoint 카운트 정리

| 영역 | endpoints |
|---|---|
| Device | 3 (list + connect + disconnect) |
| Server | 8 (CRUD 4 + test 2 + status + reconnect) |
| Benchmark | 4 (run + status + result + SSE progress) |
| Job 관리 | 2 (delete + cancel) |
| Trace | 5 (start + stop + reparse + result + raw) |
| Scenario | 1 (run) |
| Monitoring SSE | 1 |
| Macro | 12 (CRUD 6 + gRPC 위임 6) |
| APK | 3 (list + install + uninstall) |
| Preset/Template | 13 (각 CRUD) |
| Schedule | 7 |
| Execution | 5 |
| Archive | 2 |
| Screen WS | 2 (legacy + portal 호환 path) |
| 보조 WS | 2 |
| Logcat | 2 (explore + parse) |
| Marker | 2 (explore + parse) |
| AI Log Profile | 5 (CRUD) |
| **합계** | **79** |

(portal AgentController 47 + 사이드 컨트롤러 + 보조 WS + 추가 magic path 들)

⚠ Logcat / Marker / AI Log Profile 9개는 **standalone 고유**로 portal 에 대응 컨트롤러가 없다
(gRPC RPC 도 없는 REST 전용).

## 다음

- SQLite 스키마 → [06-sqlite-schema.md](06-sqlite-schema.md)
- 잡 hook 흐름 → [04-standalone-mode.md#jobexecution-hook-라이프사이클](04-standalone-mode.md#jobexecution-hook-라이프사이클)
