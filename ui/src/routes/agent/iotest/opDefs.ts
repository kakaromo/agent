/** Operation definitions — fields, defaults, help text */

export interface OpFieldDef {
	key: string;
	label: string;
	type: 'input' | 'select' | 'textarea';
	defaultValue: string;
	choices?: string[];
	help?: string;
	placeholder?: string;
}

export interface OpDef {
	label: string;
	category: 'I/O' | 'File' | 'Control' | 'Device' | 'Flow';
	color: string;       // tailwind bg color
	fields: OpFieldDef[];
	help: string;
}

export const OP_DEFS: Record<string, OpDef> = {
	open: {
		label: 'Open',
		category: 'I/O',
		color: 'bg-blue-100',
		help: '파일 열기. fd 이름을 지정하면 여러 파일을 동시에 열 수 있음',
		fields: [
			{ key: 'path', label: 'Path', type: 'input', defaultValue: '/data/local/tmp/test/file1', placeholder: '/data/local/tmp/test/file1' },
			{ key: 'fd', label: 'FD Name', type: 'input', defaultValue: '', help: '비워두면 파일명 사용. 여러 파일: A, B 등으로 구분' },
			{
				key: 'flags', label: 'Flags', type: 'select', defaultValue: 'O_RDWR|O_CREATE',
				choices: [
					'O_RDONLY', 'O_WRONLY', 'O_RDWR',
					'O_WRONLY|O_CREATE', 'O_WRONLY|O_CREATE|O_TRUNC',
					'O_RDWR|O_CREATE', 'O_WRONLY|O_CREATE|O_DIRECT',
					'O_RDONLY|O_DIRECT', 'O_RDWR|O_DIRECT',
					'O_WRONLY|O_SYNC', 'O_WRONLY|O_APPEND'
				]
			}
		]
	},
	close: {
		label: 'Close',
		category: 'I/O',
		color: 'bg-blue-100',
		help: '파일 닫기. fd 이름으로 특정 파일 지정 가능',
		fields: [
			{ key: 'fd', label: 'FD Name', type: 'input', defaultValue: '', help: '비워두면 마지막 열린 파일' }
		]
	},
	read: {
		label: 'Read',
		category: 'I/O',
		color: 'bg-green-100',
		help: 'pread() — offset 지정 읽기',
		fields: [
			{ key: 'fd', label: 'FD Name', type: 'input', defaultValue: '', help: '비워두면 마지막 열린 파일' },
			{ key: 'offset', label: 'Offset', type: 'input', defaultValue: '0', help: '4k/1m, {{i*4096}}, random:0-1m, seq:0,4k,8k' },
			{ key: 'bs', label: 'Block Size', type: 'input', defaultValue: '4k', help: '4k, random:4k,8k,16k,64k, seq:4k,8k,16k' },
			{ key: 'count', label: 'Count', type: 'input', defaultValue: '1', help: '반복 횟수' }
		]
	},
	write: {
		label: 'Write',
		category: 'I/O',
		color: 'bg-amber-100',
		help: 'pwrite() — offset 지정 쓰기',
		fields: [
			{ key: 'fd', label: 'FD Name', type: 'input', defaultValue: '', help: '비워두면 마지막 열린 파일' },
			{ key: 'offset', label: 'Offset', type: 'input', defaultValue: '0', help: '4k/1m, {{i*4096}}, random:0-1m, seq:0,4k,8k' },
			{ key: 'bs', label: 'Block Size', type: 'input', defaultValue: '4k', help: '4k, random:4k,8k,16k,64k, seq:4k,8k,16k' },
			{ key: 'count', label: 'Count', type: 'input', defaultValue: '1' },
			{ key: 'pattern', label: 'Pattern', type: 'select', defaultValue: 'zero', choices: ['zero', 'random', 'byte:0xFF', 'byte:0x55', 'byte:0xAA'] }
		]
	},
	verify: {
		label: 'Verify',
		category: 'I/O',
		color: 'bg-cyan-100',
		help: 'write한 패턴을 read해서 byte 비교. 불일치 시 에러 + mismatch 위치 보고',
		fields: [
			{ key: 'fd', label: 'FD Name', type: 'input', defaultValue: '', help: '비워두면 마지막 열린 파일' },
			{ key: 'offset', label: 'Offset', type: 'input', defaultValue: '0', help: '4k, random:0-1m' },
			{ key: 'bs', label: 'Block Size', type: 'input', defaultValue: '4k', help: 'random:4k,8k,16k' },
			{ key: 'count', label: 'Count', type: 'input', defaultValue: '1' },
			{ key: 'pattern', label: 'Pattern', type: 'select', defaultValue: 'zero', choices: ['zero', 'random', 'byte:0xFF', 'byte:0x55', 'byte:0xAA'] }
		]
	},
	fsync: {
		label: 'Fsync',
		category: 'I/O',
		color: 'bg-blue-100',
		help: 'fsync() — 디스크에 쓰기 동기화',
		fields: [
			{ key: 'fd', label: 'FD Name', type: 'input', defaultValue: '', help: '비워두면 마지막 열린 파일' }
		]
	},
	fdatasync: {
		label: 'Fdatasync',
		category: 'I/O',
		color: 'bg-blue-100',
		help: 'fdatasync() — 데이터만 동기화 (메타데이터 제외)',
		fields: [
			{ key: 'fd', label: 'FD Name', type: 'input', defaultValue: '', help: '비워두면 마지막 열린 파일' }
		]
	},
	stat: {
		label: 'Stat',
		category: 'File',
		color: 'bg-gray-100',
		help: '파일 정보 조회',
		fields: [
			{ key: 'path', label: 'Path', type: 'input', defaultValue: '' }
		]
	},
	truncate: {
		label: 'Truncate',
		category: 'File',
		color: 'bg-gray-100',
		help: '파일 크기 변경',
		fields: [
			{ key: 'path', label: 'Path', type: 'input', defaultValue: '' },
			{ key: 'size', label: 'Size', type: 'input', defaultValue: '0' }
		]
	},
	unlink: {
		label: 'Unlink',
		category: 'File',
		color: 'bg-red-100',
		help: '파일 삭제',
		fields: [
			{ key: 'path', label: 'Path', type: 'input', defaultValue: '' }
		]
	},
	rename: {
		label: 'Rename',
		category: 'File',
		color: 'bg-gray-100',
		help: '파일/디렉토리 이름 변경',
		fields: [
			{ key: 'path', label: 'Old Path', type: 'input', defaultValue: '' },
			{ key: 'new_path', label: 'New Path', type: 'input', defaultValue: '' }
		]
	},
	fallocate: {
		label: 'Fallocate',
		category: 'File',
		color: 'bg-gray-100',
		help: '파일 공간 사전 할당. fd 또는 path로 지정',
		fields: [
			{ key: 'fd', label: 'FD Name', type: 'input', defaultValue: '', help: 'fd가 있으면 열린 파일에 적용, 없으면 path로 새 파일 생성' },
			{ key: 'path', label: 'Path', type: 'input', defaultValue: '', help: 'fd 미지정 시 이 경로에 파일 생성' },
			{ key: 'size', label: 'Size', type: 'input', defaultValue: '1m' }
		]
	},
	mkdir: {
		label: 'Mkdir',
		category: 'File',
		color: 'bg-gray-100',
		help: '디렉토리 생성',
		fields: [
			{ key: 'path', label: 'Path', type: 'input', defaultValue: '/data/local/tmp/test' }
		]
	},
	create_files: {
		label: 'Create Files',
		category: 'File',
		color: 'bg-emerald-100',
		help: 'N개 파일 생성 (순차 번호)',
		fields: [
			{ key: 'dir', label: 'Directory', type: 'input', defaultValue: '/data/local/tmp/test' },
			{ key: 'prefix', label: 'Prefix', type: 'input', defaultValue: 'file_' },
			{ key: 'count', label: 'File Count', type: 'input', defaultValue: '50' },
			{ key: 'bs', label: 'Block Size', type: 'input', defaultValue: '4k' },
			{ key: 'blocks', label: 'Blocks/File', type: 'input', defaultValue: '256', help: '파일당 블록 수' }
		]
	},
	delete_pattern: {
		label: 'Delete Pattern',
		category: 'File',
		color: 'bg-red-100',
		help: '패턴에 따라 파일 삭제 (짝수/홀수/랜덤)',
		fields: [
			{ key: 'dir', label: 'Directory', type: 'input', defaultValue: '/data/local/tmp/test' },
			{ key: 'prefix', label: 'Prefix', type: 'input', defaultValue: 'file_' },
			{ key: 'count', label: 'File Count', type: 'input', defaultValue: '50' },
			{ key: 'rule', label: 'Rule', type: 'select', defaultValue: 'odd', choices: ['odd', 'even', 'random_half', 'all'] }
		]
	},
	sysfs_write: {
		label: 'Sysfs Write',
		category: 'Device',
		color: 'bg-purple-100',
		help: 'sysfs 파일에 값 쓰기 (echo)',
		fields: [
			{ key: 'path', label: 'sysfs Path', type: 'input', defaultValue: '/sys/devices/...', placeholder: '/sys/devices/.../voltage_swing' },
			{ key: 'value', label: 'Value', type: 'input', defaultValue: '0x0', help: '템플릿 가능: {{item}}' }
		]
	},
	sysfs_read: {
		label: 'Sysfs Read',
		category: 'Device',
		color: 'bg-purple-100',
		help: 'sysfs 파일 값 읽기',
		fields: [
			{ key: 'path', label: 'sysfs Path', type: 'input', defaultValue: '/sys/devices/...' }
		]
	},
	shell: {
		label: 'Shell',
		category: 'Control',
		color: 'bg-gray-100',
		help: '임의 shell 명령 실행',
		fields: [
			{ key: 'cmd', label: 'Command', type: 'textarea', defaultValue: '' }
		]
	},
	sleep: {
		label: 'Sleep',
		category: 'Control',
		color: 'bg-yellow-100',
		help: '대기 (밀리초)',
		fields: [
			{ key: 'ms', label: 'Duration (ms)', type: 'input', defaultValue: '1000' }
		]
	},
	loop: {
		label: 'Loop',
		category: 'Flow',
		color: 'bg-indigo-100',
		help: '반복 실행. count 또는 duration(초) 중 하나 지정. {{i}}=인덱스, {{item}}=items 배열',
		fields: [
			{ key: 'loop_count', label: 'Count', type: 'input', defaultValue: '10', help: '반복 횟수 (duration과 택1)' },
			{ key: 'loop_duration', label: 'Duration (sec)', type: 'input', defaultValue: '', help: '초 단위. 설정하면 count 무시하고 시간 기반 반복' },
			{ key: 'items', label: 'Items (comma)', type: 'input', defaultValue: '', help: '쉼표 구분. 예: 0x0,0x1,0x2' }
		]
	},
	if: {
		label: 'If',
		category: 'Flow',
		color: 'bg-orange-100',
		help: '조건 분기. 산술: {{i % 2 == 0}}, sysfs: {{sysfs:/path == "val"}}',
		fields: [
			{ key: 'condition', label: 'Condition', type: 'input', defaultValue: '{{i % 2 == 0}}', help: '{{i % 2 == 0}}, {{i > 25}}' }
		]
	}
};

export const OP_CATEGORIES = ['I/O', 'File', 'Device', 'Control', 'Flow'] as const;

export function getOpsByCategory(category: string): string[] {
	return Object.entries(OP_DEFS)
		.filter(([, def]) => def.category === category)
		.map(([key]) => key);
}
