<script lang="ts">
	import { STEP_TYPE_COLORS } from './types.js';
	import PlayIcon from '@lucide/svelte/icons/play';
	import TerminalIcon from '@lucide/svelte/icons/terminal';
	import TrashIcon from '@lucide/svelte/icons/trash-2';
	import ClockIcon from '@lucide/svelte/icons/clock';
	import ScanSearchIcon from '@lucide/svelte/icons/scan-search';
	import SquareIcon from '@lucide/svelte/icons/square';
	import SmartphoneIcon from '@lucide/svelte/icons/smartphone';
	import RepeatIcon from '@lucide/svelte/icons/repeat';
	import GitBranchIcon from '@lucide/svelte/icons/git-branch';
	import FlaskConicalIcon from '@lucide/svelte/icons/flask-conical';
	import DownloadIcon from '@lucide/svelte/icons/download';
	import PackageMinusIcon from '@lucide/svelte/icons/package-minus';
	import MousePointerClickIcon from '@lucide/svelte/icons/mouse-pointer-click';
	import TypeIcon from '@lucide/svelte/icons/type';

	const stepTypes = [
		{ type: 'benchmark', label: 'Benchmark', icon: PlayIcon, desc: 'fio/iozone/tiotest' },
		{ type: 'iotest', label: 'I/O Test', icon: FlaskConicalIcon, desc: 'syscall I/O 테스트' },
		{ type: 'shell', label: 'Shell', icon: TerminalIcon, desc: '쉘 명령어' },
		{ type: 'cleanup', label: 'Cleanup', icon: TrashIcon, desc: '파일 삭제' },
		{ type: 'sleep', label: 'Sleep', icon: ClockIcon, desc: '대기' },
		{ type: 'trace_start', label: 'Trace Start', icon: ScanSearchIcon, desc: 'ftrace 시작' },
		{ type: 'trace_stop', label: 'Trace Stop', icon: SquareIcon, desc: 'ftrace 중지' },
		{ type: 'app_macro', label: 'App Macro', icon: SmartphoneIcon, desc: '앱 매크로 실행' },
		{ type: 'tap_element', label: 'Tap Element', icon: MousePointerClickIcon, desc: '요소 기반 탭' },
		{ type: 'text', label: 'Text Input', icon: TypeIcon, desc: '텍스트 입력' },
		{ type: 'install_apk', label: 'Install APK', icon: DownloadIcon, desc: 'APK 설치' },
		{ type: 'uninstall_apk', label: 'Uninstall APK', icon: PackageMinusIcon, desc: '앱 제거' }
	];

	function onDragStart(event: DragEvent, type: string) {
		if (!event.dataTransfer) return;
		event.dataTransfer.setData('application/step-type', type);
		event.dataTransfer.effectAllowed = 'move';
	}
</script>

<div class="w-28 shrink-0 border-r p-1.5 space-y-0.5 overflow-y-auto text-[10px] leading-tight">
	<div class="text-[8px] font-medium text-muted-foreground uppercase tracking-wider mb-1">Step Types</div>
	<!-- Loop group -->
	<div class="text-[8px] font-medium text-muted-foreground uppercase tracking-wider mb-0.5 mt-1.5">Control</div>
	<div
		draggable="true"
		ondragstart={(e) => onDragStart(e, '__loop__')}
		class="flex items-center gap-1 px-1.5 py-1 rounded border border-blue-300 cursor-grab hover:bg-blue-50 active:cursor-grabbing transition-colors"
	>
		<RepeatIcon class="size-2.5 text-blue-600 shrink-0" />
		<div class="min-w-0">
			<div class="text-[9px] font-medium truncate">Loop</div>
			<div class="text-[8px] text-muted-foreground truncate">반복 그룹</div>
		</div>
	</div>

	<div
		draggable="true"
		ondragstart={(e) => onDragStart(e, '__condition__')}
		class="flex items-center gap-1 px-1.5 py-1 rounded border border-amber-300 cursor-grab hover:bg-amber-50 active:cursor-grabbing transition-colors"
	>
		<GitBranchIcon class="size-2.5 text-amber-600 shrink-0" />
		<div class="min-w-0">
			<div class="text-[9px] font-medium truncate">Condition</div>
			<div class="text-[8px] text-muted-foreground truncate">조건 분기</div>
		</div>
	</div>

	<div class="border-t my-1.5"></div>

	{#each stepTypes as st}
		{@const colors = STEP_TYPE_COLORS[st.type] ?? STEP_TYPE_COLORS.shell}
		<div
			draggable="true"
			ondragstart={(e) => onDragStart(e, st.type)}
			class="flex items-center gap-1 px-1.5 py-1 rounded border cursor-grab hover:bg-muted/50 active:cursor-grabbing transition-colors"
		>
			<st.icon class="size-2.5 shrink-0 {colors.text}" />
			<div class="min-w-0">
				<div class="text-[9px] font-medium truncate">{st.label}</div>
				<div class="text-[8px] text-muted-foreground truncate">{st.desc}</div>
			</div>
		</div>
	{/each}
</div>
