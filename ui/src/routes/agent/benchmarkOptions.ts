/**
 * Tool별 벤치마크 옵션 정의 + 도움말
 * BenchmarkForm, ScenarioBuilder에서 공유
 */

export interface OptionDef {
	key: string;
	label: string;
	type: 'select' | 'input' | 'checkbox';
	defaultValue: string;
	choices?: string[];
	unit?: string;
	help: string;
	group: 'basic' | 'advanced';
}

// ══════════════════════════════════════════
// fio
// ══════════════════════════════════════════

export const FIO_OPTIONS: OptionDef[] = [
	// ── Basic ──
	{
		key: 'bs', label: 'Block Size', type: 'select', defaultValue: '4k', group: 'basic',
		choices: ['512', '1k', '2k', '4k', '8k', '16k', '32k', '64k', '128k', '256k', '512k', '1m'],
		help: '한 번의 I/O 작업에서 읽거나 쓰는 데이터 크기. 작을수록 IOPS 높고, 클수록 대역폭 높음.'
	},
	{
		key: 'rw', label: 'I/O Pattern', type: 'select', defaultValue: 'randread', group: 'basic',
		choices: ['read', 'write', 'randread', 'randwrite', 'readwrite', 'randrw', 'trim', 'randtrim', 'trimwrite'],
		help: 'I/O 접근 패턴. read=순차 읽기, randread=랜덤 읽기, readwrite=순차 혼합, randrw=랜덤 혼합, trim=순차 trim.'
	},
	{
		key: 'size', label: 'I/O Size', type: 'input', defaultValue: '1g', group: 'basic',
		help: '테스트할 파일의 총 크기. runtime 없이 size만 지정하면 파일 전체 I/O 후 종료. 예: 100m, 1g, 10g'
	},
	{
		key: 'runtime', label: 'Runtime', type: 'input', defaultValue: '', unit: 'sec', group: 'basic',
		help: '테스트 실행 시간(초). 비워두면 size만큼 완료될 때까지 실행. 값 입력 시 time_based도 활성화 권장.'
	},
	{
		key: 'numjobs', label: 'Num Jobs', type: 'input', defaultValue: '1', group: 'basic',
		help: '동시에 실행할 독립 I/O 프로세스 수. 멀티스레드 성능 측정에 사용.'
	},
	{
		key: 'ioengine', label: 'I/O Engine', type: 'select', defaultValue: 'libaio', group: 'basic',
		choices: ['libaio', 'sync', 'psync', 'io_uring', 'mmap', 'posixaio', 'windowsaio'],
		help: 'I/O 시스템콜 방식. libaio=Linux AIO, io_uring=최신 고성능, sync=동기, posixaio=POSIX AIO.'
	},
	{
		key: 'direct', label: 'Direct I/O', type: 'checkbox', defaultValue: '1', group: 'basic',
		help: 'OS 페이지 캐시를 우회하여 디스크에 직접 I/O (O_DIRECT). 스토리지 성능 측정 시 필수.'
	},
	{
		key: 'iodepth', label: 'I/O Depth', type: 'input', defaultValue: '32', group: 'basic',
		help: '비동기 I/O 큐에 대기시킬 요청 수. 높을수록 디바이스 활용도 증가. libaio/io_uring에서 유효.'
	},

	// ── Advanced: Mixed Workload ──
	{
		key: 'rwmixread', label: 'Read Mix %', type: 'input', defaultValue: '', group: 'advanced',
		help: 'readwrite/randrw에서 Read 비율(%). 예: 70이면 Read 70%, Write 30%. rw=randrw일 때만 유효. 비워두면 fio 기본값(50) 사용.'
	},
	{
		key: 'rwmixwrite', label: 'Write Mix %', type: 'input', defaultValue: '', group: 'advanced',
		help: 'readwrite/randrw에서 Write 비율(%). rwmixread와 합이 100이 아니어도 됨 (fio가 조정).'
	},

	// ── Advanced: Block Size 세분화 ──
	{
		key: 'bsrange', label: 'BS Range', type: 'input', defaultValue: '', group: 'advanced',
		help: '블록 크기 범위. 예: 4k-64k. 이 범위 내에서 랜덤 블록 크기 사용. bs 대신 사용.'
	},
	{
		key: 'bssplit', label: 'BS Split', type: 'input', defaultValue: '', group: 'advanced',
		help: '블록 크기별 비율 지정. 예: 4k/50:64k/30:128k/20 → 4k 50%, 64k 30%, 128k 20%. 정밀 워크로드 시뮬레이션.'
	},

	// ── Advanced: Zone/Chunk ──
	{
		key: 'zonesize', label: 'Zone Size', type: 'input', defaultValue: '', group: 'advanced',
		help: '작업 영역(zone) 크기. zonerange와 함께 사용하여 특정 영역만 I/O. 예: 256m'
	},
	{
		key: 'zonerange', label: 'Zone Range', type: 'input', defaultValue: '', group: 'advanced',
		help: '각 zone 내에서 I/O할 범위. zonesize 안에서 이 크기만큼만 접근. 예: 64m'
	},
	{
		key: 'zoneskip', label: 'Zone Skip', type: 'input', defaultValue: '', group: 'advanced',
		help: 'zone 완료 후 건너뛸 크기. 디스크의 특정 위치만 테스트할 때 사용.'
	},
	{
		key: 'chunk_size', label: 'Chunk Size', type: 'input', defaultValue: '', group: 'advanced',
		help: 'RAID 스트라이프 크기 등에 맞춰 I/O를 정렬. 예: 64k'
	},
	{
		key: 'offset', label: 'Offset', type: 'input', defaultValue: '', group: 'advanced',
		help: '파일 내 시작 오프셋. 예: 1g → 1GB 지점부터 I/O 시작.'
	},

	// ── Advanced: Timing & Control ──
	{
		key: 'time_based', label: 'Time Based', type: 'checkbox', defaultValue: '0', group: 'advanced',
		help: 'runtime 동안 I/O 반복. 비활성이면 size만큼 한 번만 실행 후 종료. runtime 사용 시 활성화 권장.'
	},
	{
		key: 'ramp_time', label: 'Ramp Time', type: 'input', defaultValue: '', unit: 'sec', group: 'advanced',
		help: '측정 전 워밍업 시간(초). 이 시간 동안의 결과는 통계에서 제외.'
	},
	{
		key: 'thinktime', label: 'Think Time', type: 'input', defaultValue: '', unit: 'us', group: 'advanced',
		help: 'I/O 작업 사이 대기 시간(마이크로초). 실제 애플리케이션 패턴 시뮬레이션.'
	},
	{
		key: 'rate', label: 'Rate Limit', type: 'input', defaultValue: '', group: 'advanced',
		help: 'I/O 속도 제한. 예: 10m → 10MB/s로 제한. 또는 rate_iops=1000.'
	},
	{
		key: 'rate_iops', label: 'IOPS Limit', type: 'input', defaultValue: '', group: 'advanced',
		help: 'IOPS 제한. 예: 1000 → 초당 1000 IOPS로 제한.'
	},

	// ── Advanced: Verification ──
	{
		key: 'verify', label: 'Verify', type: 'select', defaultValue: '', group: 'advanced',
		choices: ['', 'md5', 'crc32', 'crc32c', 'sha1', 'sha256', 'sha512', 'pattern'],
		help: '쓰기 후 데이터 무결성 검증 방식. md5/crc32 등. 빈 값이면 검증 안 함.'
	},

	// ── Advanced: Reporting ──
	{
		key: 'group_reporting', label: 'Group Report', type: 'checkbox', defaultValue: '1', group: 'advanced',
		help: '모든 job의 결과를 하나로 합산 보고. 비활성이면 job별 개별 보고.'
	},
	{
		key: 'log_avg_msec', label: 'Log Interval', type: 'input', defaultValue: '', unit: 'ms', group: 'advanced',
		help: 'BW/IOPS/latency 로그 기록 간격(ms). 예: 1000 → 1초마다 기록.'
	}
];

// ══════════════════════════════════════════
// iozone
// ══════════════════════════════════════════

export const IOZONE_OPTIONS: OptionDef[] = [
	{
		key: 's', label: 'File Size', type: 'input', defaultValue: '1g', group: 'basic',
		help: '테스트 파일 크기 (-s). kB 단위, 또는 #k/#m/#g 접미사. 예: 1g, 512m'
	},
	{
		key: 'r', label: 'Record Size', type: 'select', defaultValue: '4k', group: 'basic',
		choices: ['1k', '2k', '4k', '8k', '16k', '32k', '64k', '128k', '256k', '512k', '1m'],
		help: '한 번에 읽거나 쓰는 레코드 크기 (-r). kB 단위.'
	},
	{
		key: 'i', label: 'Test Type', type: 'select', defaultValue: '0', group: 'basic',
		choices: ['0', '1', '2', '3', '4', '5', '6', '7', '8', '9', '10', '11', '12'],
		help: '테스트 종류 (-i). 0=write/rewrite, 1=read/re-read, 2=random-read/write, 3=read-backwards, 4=re-write-record, 5=stride-read, 6=fwrite/fread, 8=random_mix. 여러 번 지정 가능.'
	},
	{
		key: 't', label: 'Threads', type: 'input', defaultValue: '1', group: 'basic',
		help: '처리량 테스트에 사용할 스레드/프로세스 수 (-t).'
	},
	{
		key: 'I', label: 'Direct I/O', type: 'checkbox', defaultValue: '0', group: 'basic',
		help: 'O_DIRECT 사용 (-I). 버퍼 캐시를 우회하여 직접 I/O.'
	},
	{
		key: 'a', label: 'Auto Mode', type: 'checkbox', defaultValue: '0', group: 'advanced',
		help: '자동 모드 (-a). 다양한 파일/레코드 크기 조합을 자동 테스트.'
	},
	{
		key: 'e', label: 'Include Flush', type: 'checkbox', defaultValue: '0', group: 'advanced',
		help: 'fsync/fflush를 타이밍 계산에 포함 (-e).'
	},
	{
		key: 'c', label: 'Include Close', type: 'checkbox', defaultValue: '0', group: 'advanced',
		help: 'close()를 타이밍 계산에 포함 (-c).'
	},
	{
		key: 'O', label: 'Ops/sec', type: 'checkbox', defaultValue: '0', group: 'advanced',
		help: '결과를 ops/sec 단위로 출력 (-O).'
	},
	{
		key: 'g', label: 'Max File Size', type: 'input', defaultValue: '', group: 'advanced',
		help: '자동 모드에서 최대 파일 크기 (-g). kB 단위, #m/#g 접미사.'
	},
	{
		key: 'n', label: 'Min File Size', type: 'input', defaultValue: '', group: 'advanced',
		help: '자동 모드에서 최소 파일 크기 (-n). kB 단위.'
	},
	{
		key: 'y', label: 'Min Rec Size', type: 'input', defaultValue: '', group: 'advanced',
		help: '자동 모드에서 최소 레코드 크기 (-y). kB 단위.'
	},
	{
		key: 'q', label: 'Max Rec Size', type: 'input', defaultValue: '', group: 'advanced',
		help: '자동 모드에서 최대 레코드 크기 (-q). kB 단위.'
	}
];

// ══════════════════════════════════════════
// tiotest
// ══════════════════════════════════════════

export const TIOTEST_OPTIONS: OptionDef[] = [
	{
		key: 'test', label: 'Test Type', type: 'select', defaultValue: 'all', group: 'basic',
		choices: ['all', 'seq_write', 'seq_read', 'rand_write', 'rand_read', 'seq', 'rand'],
		help: '실행할 테스트 선택. all=전부, seq_write=순차 쓰기만, seq_read=순차 읽기만, rand_write=랜덤 쓰기만, rand_read=랜덤 읽기만, seq=순차 쓰기+읽기, rand=랜덤 쓰기+읽기.'
	},
	{
		key: 'f', label: 'File Size', type: 'input', defaultValue: '10', unit: 'MB', group: 'basic',
		help: '스레드당 테스트 파일 크기 (-f). MB 단위. 기본값 10.'
	},
	{
		key: 'b', label: 'Block Size', type: 'select', defaultValue: '4096', group: 'basic',
		choices: ['512', '1024', '2048', '4096', '8192', '16384', '32768', '65536', '131072', '262144', '524288', '1048576', '2097152'],
		help: '블록 크기 (-b). 바이트 단위. 기본값 4096.'
	},
	{
		key: 't', label: 'Threads', type: 'input', defaultValue: '4', group: 'basic',
		help: '동시 테스트 스레드 수 (-t). 기본값 4.'
	},
	{
		key: 'r', label: 'Random Ops', type: 'input', defaultValue: '100000', group: 'basic',
		help: '스레드당 랜덤 I/O 작업 수 (-r). 기본값 100000.'
	},
	{
		key: 'I', label: 'Direct I/O', type: 'checkbox', defaultValue: '0', group: 'basic',
		help: 'Direct I/O 사용 (-I). 버퍼 캐시 우회하여 디스크에 직접 I/O.'
	},
	{
		key: 'W', label: 'Seq Write', type: 'checkbox', defaultValue: '0', group: 'advanced',
		help: '쓰기 단계를 순차적으로 실행 (-W).'
	},
	{
		key: 'S', label: 'Sync Write', type: 'checkbox', defaultValue: '0', group: 'advanced',
		help: '동기 쓰기 모드 (-S).'
	},
	{
		key: 'F', label: 'Flush Cache', type: 'checkbox', defaultValue: '0', group: 'advanced',
		help: '테스트 전 OS 캐시 플러시 (-F). root 권한 필요.'
	},
	{
		key: 'c', label: 'Consistency', type: 'checkbox', defaultValue: '0', group: 'advanced',
		help: '데이터 일관성 검증 (-c). I/O 속도 감소, CPU 사용 증가.'
	}
];

// ══════════════════════════════════════════
// Utility functions
// ══════════════════════════════════════════

export function getOptionsForTool(tool: string): OptionDef[] {
	switch (tool.toUpperCase()) {
		case 'FIO': return FIO_OPTIONS;
		case 'IOZONE': return IOZONE_OPTIONS;
		case 'TIOTEST': return TIOTEST_OPTIONS;
		case 'IOTEST': return []; // IOTestEditor 사용
		default: return FIO_OPTIONS;
	}
}

export function getBasicOptions(tool: string): OptionDef[] {
	return getOptionsForTool(tool).filter(o => o.group === 'basic');
}

export function getAdvancedOptions(tool: string): OptionDef[] {
	return getOptionsForTool(tool).filter(o => o.group === 'advanced');
}

export function getDefaultParams(tool: string): Record<string, string> {
	const opts = getOptionsForTool(tool);
	const params: Record<string, string> = {};
	for (const opt of opts) {
		if (opt.defaultValue) params[opt.key] = opt.defaultValue;
	}
	return params;
}

/**
 * 폼 필드 값 + 추가 textarea 파라미터를 합침.
 * 빈 값은 제외. 폼 필드가 우선.
 */
export function mergeParams(formParams: Record<string, string>, extraText: string): Record<string, string> {
	const extra: Record<string, string> = {};
	for (const line of extraText.split('\n')) {
		const t = line.trim();
		if (!t || !t.includes('=')) continue;
		const [k, ...rest] = t.split('=');
		extra[k.trim()] = rest.join('=').trim();
	}
	// Merge: extra first, then form overrides. Remove empty/placeholder values.
	const merged = { ...extra, ...formParams };
	for (const [k, v] of Object.entries(merged)) {
		if (!v || v === 'all') delete merged[k];
	}
	return merged;
}
