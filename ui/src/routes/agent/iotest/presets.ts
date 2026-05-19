/** Preset command sequences for common I/O test patterns */

import type { IOTestThread } from './types.js';

export type IOTestPresetCategory = 'Basic I/O' | 'Random/Stress' | 'Data Integrity' | 'File Management' | 'Concurrent' | 'Device Control';

export interface IOTestPreset {
	id: string;
	label: string;
	description: string;
	category: IOTestPresetCategory;
	threads: IOTestThread[];
}

export const IOTEST_PRESETS: IOTestPreset[] = [
	{
		id: 'offset_write',
		label: 'Offset Write',
		description: 'offset 0부터 4k씩 이동하며 순차 쓰기',
		category: 'Basic I/O',
		threads: [{
			name: 'offset_writer',
			commands: [
				{ op: 'open', path: '/data/local/tmp/test/file1', flags: 'O_WRONLY|O_CREATE|O_TRUNC' },
				{ op: 'loop', loop_count: 256, commands: [
					{ op: 'write', offset: '{{i*4096}}', bs: '4k', count: 1, pattern: 'zero' }
				]},
				{ op: 'fsync' },
				{ op: 'close' }
			]
		}]
	},
	{
		id: 'offset_read_write',
		label: 'Offset R/W',
		description: 'offset 이동하며 write 후 같은 offset에서 read 검증',
		category: 'Basic I/O',
		threads: [{
			name: 'offset_rw',
			commands: [
				{ op: 'open', path: '/data/local/tmp/test/file1', flags: 'O_RDWR|O_CREATE|O_TRUNC' },
				{ op: 'loop', loop_count: 100, commands: [
					{ op: 'write', offset: '{{i*4096}}', bs: '4k', count: 1, pattern: 'random' },
					{ op: 'read', offset: '{{i*4096}}', bs: '4k', count: 1 }
				]},
				{ op: 'close' }
			]
		}]
	},
	{
		id: 'misalign_rw',
		label: 'Misaligned R/W',
		description: '비정렬 offset(512B 단위)에서 read/write',
		category: 'Basic I/O',
		threads: [{
			name: 'misalign',
			commands: [
				{ op: 'open', path: '/data/local/tmp/test/file1', flags: 'O_RDWR|O_CREATE|O_TRUNC' },
				{ op: 'loop', loop_count: 200, commands: [
					{ op: 'write', offset: '{{i*512}}', bs: '512', count: 1, pattern: 'random' }
				]},
				{ op: 'loop', loop_count: 200, commands: [
					{ op: 'read', offset: '{{i*512}}', bs: '512', count: 1 }
				]},
				{ op: 'close' }
			]
		}]
	},
	{
		id: 'create_delete_odd',
		label: 'Create + Delete Odd',
		description: '파일 50개 생성 → 홀수 번호 삭제',
		category: 'File Management',
		threads: [{
			name: 'file_ops',
			commands: [
				{ op: 'create_files', dir: '/data/local/tmp/test/sub', prefix: 'file_', count: 50, bs: '4k', blocks: 256 },
				{ op: 'delete_pattern', dir: '/data/local/tmp/test/sub', prefix: 'file_', rule: 'odd', count: 50 }
			]
		}]
	},
	{
		id: 'create_delete_even',
		label: 'Create + Delete Even',
		description: '파일 50개 생성 → 짝수 번호 삭제',
		category: 'File Management',
		threads: [{
			name: 'file_ops',
			commands: [
				{ op: 'create_files', dir: '/data/local/tmp/test/sub', prefix: 'file_', count: 50, bs: '4k', blocks: 256 },
				{ op: 'delete_pattern', dir: '/data/local/tmp/test/sub', prefix: 'file_', rule: 'even', count: 50 }
			]
		}]
	},
	{
		id: 'mixed_rwd',
		label: 'Mixed Read/Write/Delete',
		description: '3 threads: sequential write + random read + 파일 생성/삭제',
		category: 'Concurrent',
		threads: [
			{
				name: 'writer',
				commands: [
					{ op: 'open', path: '/data/local/tmp/test/file1', flags: 'O_WRONLY|O_CREATE|O_TRUNC' },
					{ op: 'loop', loop_count: 256, commands: [
						{ op: 'write', offset: '{{i*4096}}', bs: '4k', count: 1, pattern: 'zero' }
					]},
					{ op: 'fsync' },
					{ op: 'close' }
				]
			},
			{
				name: 'reader',
				commands: [
					{ op: 'sleep', ms: 500 },
					{ op: 'open', path: '/data/local/tmp/test/file1', flags: 'O_RDONLY' },
					{ op: 'loop', loop_count: 256, commands: [
						{ op: 'read', offset: '{{i*4096}}', bs: '4k', count: 1 }
					]},
					{ op: 'close' }
				]
			},
			{
				name: 'file_ops',
				commands: [
					{ op: 'create_files', dir: '/data/local/tmp/test/files', prefix: 'f_', count: 30, bs: '4k', blocks: 64 },
					{ op: 'delete_pattern', dir: '/data/local/tmp/test/files', prefix: 'f_', rule: 'odd', count: 30 }
				]
			}
		]
	},
	{
		id: 'voltage_swing',
		label: 'Voltage Swing + I/O',
		description: '2 threads: sysfs voltage 변경 + 동시 write',
		category: 'Device Control',
		threads: [
			{
				name: 'voltage_swing',
				commands: [
					{ op: 'loop', loop_count: 4, items: ['0x0', '0x1', '0x2', '0x3'], commands: [
						{ op: 'sysfs_write', path: '/sys/devices/.../voltage_swing', value: '{{item}}' },
						{ op: 'sleep', ms: 1000 }
					]}
				]
			},
			{
				name: 'writer',
				commands: [
					{ op: 'open', path: '/data/local/tmp/test/file1', flags: 'O_WRONLY|O_CREATE|O_TRUNC' },
					{ op: 'loop', loop_count: 100, commands: [
						{ op: 'write', offset: '{{i*4096}}', bs: '4k', count: 1, pattern: 'random' }
					]},
					{ op: 'close' }
				]
			}
		]
	},
	{
		id: 'conditional_rw',
		label: 'Conditional R/W',
		description: 'loop에서 짝수=write, 홀수=read',
		category: 'Basic I/O',
		threads: [{
			name: 'cond_rw',
			commands: [
				{ op: 'open', path: '/data/local/tmp/test/file1', flags: 'O_RDWR|O_CREATE|O_TRUNC' },
				{ op: 'write', offset: '0', bs: '4k', count: 50 },
				{ op: 'loop', loop_count: 50, commands: [
					{ op: 'if', condition: '{{i % 2 == 0}}',
						then: [{ op: 'write', offset: '{{i*4096}}', bs: '4k', count: 1, pattern: 'random' }],
						else: [{ op: 'read', offset: '{{i*4096}}', bs: '4k', count: 1 }]
					}
				]},
				{ op: 'close' }
			]
		}]
	},
	{
		id: 'multi_fd_rw',
		label: 'Multi-File R/W',
		description: '2개 파일 동시 open → 교차 write/read + verify',
		category: 'Data Integrity',
		threads: [{
			name: 'multi_fd',
			commands: [
				{ op: 'open', path: '/data/local/tmp/test/fileA', flags: 'O_RDWR|O_CREATE|O_TRUNC', fd: 'A' },
				{ op: 'open', path: '/data/local/tmp/test/fileB', flags: 'O_RDWR|O_CREATE|O_TRUNC', fd: 'B' },
				{ op: 'write', fd: 'A', offset: '0', bs: '4k', count: 100, pattern: 'byte:0xAA' },
				{ op: 'write', fd: 'B', offset: '0', bs: '4k', count: 100, pattern: 'byte:0x55' },
				{ op: 'fsync', fd: 'A' },
				{ op: 'fsync', fd: 'B' },
				{ op: 'verify', fd: 'A', offset: '0', bs: '4k', count: 100, pattern: 'byte:0xAA' },
				{ op: 'verify', fd: 'B', offset: '0', bs: '4k', count: 100, pattern: 'byte:0x55' },
				{ op: 'close', fd: 'A' },
				{ op: 'close', fd: 'B' }
			]
		}]
	},
	{
		id: 'random_bs_verify',
		label: 'Random BS + Verify',
		description: '랜덤 블록 크기로 write → 같은 크기로 verify',
		category: 'Data Integrity',
		threads: [{
			name: 'random_verify',
			commands: [
				{ op: 'open', path: '/data/local/tmp/test/file1', flags: 'O_RDWR|O_CREATE|O_TRUNC' },
				{ op: 'fallocate', size: '10m' },
				{ op: 'loop', loop_count: 50, commands: [
					{ op: 'write', offset: '{{i*4096}}', bs: 'random:4k,8k,16k', count: 1, pattern: 'byte:0xAA' }
				]},
				{ op: 'fsync' },
				{ op: 'loop', loop_count: 50, commands: [
					{ op: 'verify', offset: '{{i*4096}}', bs: '4k', count: 1, pattern: 'byte:0xAA' }
				]},
				{ op: 'close' }
			]
		}]
	},
	{
		id: 'fragmentation',
		label: 'Fragmentation Test',
		description: '파일 100개 생성 → 홀수 삭제 → 빈 공간에 다른 크기 파일 → R/W',
		category: 'File Management',
		threads: [{
			name: 'frag_test',
			commands: [
				{ op: 'create_files', dir: '/data/local/tmp/test/frag', prefix: 'f_', count: 100, bs: '4k', blocks: 64 },
				{ op: 'delete_pattern', dir: '/data/local/tmp/test/frag', prefix: 'f_', rule: 'odd', count: 100 },
				{ op: 'create_files', dir: '/data/local/tmp/test/frag', prefix: 'big_', count: 25, bs: '16k', blocks: 64 },
				{ op: 'open', path: '/data/local/tmp/test/frag/big_1', flags: 'O_RDWR' },
				{ op: 'loop', loop_count: 64, commands: [
					{ op: 'write', offset: '{{i*16384}}', bs: '16k', count: 1, pattern: 'random' }
				]},
				{ op: 'fsync' },
				{ op: 'loop', loop_count: 64, commands: [
					{ op: 'read', offset: '{{i*16384}}', bs: '16k', count: 1 }
				]},
				{ op: 'close' },
				{ op: 'delete_pattern', dir: '/data/local/tmp/test/frag', prefix: 'f_', rule: 'all', count: 100 },
				{ op: 'delete_pattern', dir: '/data/local/tmp/test/frag', prefix: 'big_', rule: 'all', count: 25 }
			]
		}]
	},
	{
		id: 'cache_miss',
		label: 'Cache Miss (Seq W → Rand R)',
		description: '대용량 순차 write → random read로 캐시 미스 유도',
		category: 'Random/Stress',
		threads: [{
			name: 'cache_miss',
			commands: [
				{ op: 'open', path: '/data/local/tmp/test/bigfile', flags: 'O_RDWR|O_CREATE|O_TRUNC|O_DIRECT' },
				{ op: 'loop', loop_count: 2560, commands: [
					{ op: 'write', offset: '{{i*4096}}', bs: '4k', count: 1, pattern: 'random' }
				]},
				{ op: 'fsync' },
				{ op: 'loop', loop_count: 1000, commands: [
					{ op: 'read', offset: 'random:0-10m', bs: 'random:4k,8k,16k', count: 1 }
				]},
				{ op: 'close' }
			]
		}]
	},
	{
		id: 'duration_stress',
		label: 'Duration Stress (60s)',
		description: '60초 동안 random R/W 반복. 시간 기반 loop',
		category: 'Random/Stress',
		threads: [{
			name: 'stress_60s',
			commands: [
				{ op: 'open', path: '/data/local/tmp/test/stress', flags: 'O_RDWR|O_CREATE|O_TRUNC' },
				{ op: 'write', offset: '0', bs: '4k', count: 2560, pattern: 'zero' },
				{ op: 'loop', loop_duration: 60, commands: [
					{ op: 'write', offset: 'random:0-10m', bs: 'random:4k,8k,16k,64k', count: 1, pattern: 'random' },
					{ op: 'read', offset: 'random:0-10m', bs: 'random:4k,8k,16k,64k', count: 1 }
				]},
				{ op: 'close' }
			]
		}]
	},
	{
		id: 'concurrent_same_file',
		label: 'Concurrent Same File',
		description: '3 threads가 같은 파일에 동시 R/W + verify',
		category: 'Concurrent',
		threads: [
			{
				name: 'writer_1',
				commands: [
					{ op: 'open', path: '/data/local/tmp/test/shared', flags: 'O_RDWR|O_CREATE' },
					{ op: 'loop', loop_count: 500, commands: [
						{ op: 'write', offset: '{{i*4096}}', bs: '4k', count: 1, pattern: 'byte:0xAA' }
					]},
					{ op: 'fsync' },
					{ op: 'close' }
				]
			},
			{
				name: 'writer_2',
				commands: [
					{ op: 'sleep', ms: 100 },
					{ op: 'open', path: '/data/local/tmp/test/shared', flags: 'O_RDWR' },
					{ op: 'loop', loop_count: 500, commands: [
						{ op: 'write', offset: '{{i*4096 + 2048000}}', bs: '4k', count: 1, pattern: 'byte:0x55' }
					]},
					{ op: 'fsync' },
					{ op: 'close' }
				]
			},
			{
				name: 'reader',
				commands: [
					{ op: 'sleep', ms: 200 },
					{ op: 'open', path: '/data/local/tmp/test/shared', flags: 'O_RDONLY' },
					{ op: 'loop', loop_count: 1000, commands: [
						{ op: 'read', offset: 'random:0-4m', bs: '4k', count: 1 }
					]},
					{ op: 'close' }
				]
			}
		]
	},
	{
		id: 'rename_stress',
		label: 'Rename Stress',
		description: '파일 생성 → rename 반복 → 최종 verify',
		category: 'File Management',
		threads: [{
			name: 'rename_test',
			commands: [
				{ op: 'open', path: '/data/local/tmp/test/rename_src', flags: 'O_WRONLY|O_CREATE|O_TRUNC' },
				{ op: 'write', offset: '0', bs: '4k', count: 256, pattern: 'byte:0xBB' },
				{ op: 'fsync' },
				{ op: 'close' },
				{ op: 'loop', loop_count: 10, commands: [
					{ op: 'rename', path: '/data/local/tmp/test/rename_src', new_path: '/data/local/tmp/test/rename_dst' },
					{ op: 'rename', path: '/data/local/tmp/test/rename_dst', new_path: '/data/local/tmp/test/rename_src' }
				]},
				{ op: 'open', path: '/data/local/tmp/test/rename_src', flags: 'O_RDONLY' },
				{ op: 'verify', offset: '0', bs: '4k', count: 256, pattern: 'byte:0xBB' },
				{ op: 'close' }
			]
		}]
	}
];

export function getPresetsByCategory(category: string): IOTestPreset[] {
	return IOTEST_PRESETS.filter(p => p.category === category);
}

export const PRESET_CATEGORIES: IOTestPresetCategory[] = ['Basic I/O', 'Random/Stress', 'Data Integrity', 'File Management', 'Concurrent', 'Device Control'];
