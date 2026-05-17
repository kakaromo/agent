<script lang="ts">
	import * as Sheet from '$lib/components/ui/sheet/index.js';
	import {
		listPreCommands,
		createPreCommand,
		updatePreCommand,
		deletePreCommand,
		assignSlots,
		unassignSlots,
		hasTcPreCommands,
		type PreCommand,
		type SlotAssignment
	} from '$lib/api/preCommand.js';
	import { toast } from 'svelte-sonner';
	import PlusIcon from '@lucide/svelte/icons/plus';
	import TrashIcon from '@lucide/svelte/icons/trash-2';
	import PencilIcon from '@lucide/svelte/icons/pencil';
	import ChevronLeftIcon from '@lucide/svelte/icons/chevron-left';
	import SaveIcon from '@lucide/svelte/icons/save';
	import LoaderIcon from '@lucide/svelte/icons/loader-circle';
	import ZapIcon from '@lucide/svelte/icons/zap';
	import PlayIcon from '@lucide/svelte/icons/play';
	import CheckIcon from '@lucide/svelte/icons/check';
	import SettingsIcon from '@lucide/svelte/icons/settings';

	interface SlotInfo {
		slotIndex: number;
		slotLabel: string;
		setLocation: string;
	}

	interface Props {
		open: boolean;
		/** 선택된 슬롯 정보 */
		selectedSlots?: SlotInfo[];
		/** 현재 source (compatibility, performance 등) — 즉시 실행용 */
		source?: string;
		/** 슬롯별 등록 현황 */
		assignments?: SlotAssignment[];
		/** 즉시 실행 콜백 */
		onExecute?: (preCommand: PreCommand) => void;
		/** 등록 변경 콜백 */
		onAssignmentChanged?: () => void;
	}

	let {
		open = $bindable(),
		selectedSlots = [],
		source = '',
		assignments = [],
		onExecute,
		onAssignmentChanged
	}: Props = $props();

	let items = $state<PreCommand[]>([]);
	let loading = $state(false);

	// 뷰 모드: main(통합 뷰), manage(관리 목록), edit(편집)
	type ViewMode = 'main' | 'manage' | 'edit';
	let mode = $state<ViewMode>('main');
	let editingId = $state<number | null>(null);
	let editName = $state('');
	let editDescription = $state('');
	let editCommands = $state('');
	let saving = $state(false);

	// 선택된 슬롯들에 현재 등록된 명령어 ID
	let assignedCommandId = $derived.by(() => {
		if (selectedSlots.length === 0 || !assignments) return null;
		const ids = new Set(
			selectedSlots
				.map(s => (assignments ?? []).find(a => a.setLocation === s.setLocation)?.preCommandId)
				.filter(Boolean)
		);
		return ids.size === 1 ? [...ids][0]! : null;
	});

	let hasMixedAssignment = $derived.by(() => {
		if (selectedSlots.length <= 1 || !assignments) return false;
		const ids = selectedSlots
			.map(s => (assignments ?? []).find(a => a.setLocation === s.setLocation)?.preCommandId ?? 0);
		return new Set(ids).size > 1;
	});

	$effect(() => {
		if (open) {
			loadItems();
			mode = 'main';
		}
	});

	async function loadItems() {
		loading = true;
		try {
			items = await listPreCommands();
		} catch (e) {
			toast.error('Pre-Command 목록 조회 실패');
		} finally {
			loading = false;
		}
	}

	// ── 등록/해제 ──

	async function handleAssign(item: PreCommand) {
		if (selectedSlots.length === 0) return;
		const locs = selectedSlots.map(s => s.setLocation).filter(l => !!l);
		if (locs.length === 0) return;
		try {
			// TC Pre-Command가 있는지 확인
			const { hasTc } = await hasTcPreCommands(locs);
			if (hasTc) {
				const ok = confirm('선택한 슬롯에 TC Pre-Command가 등록되어 있습니다.\n슬롯 Pre-Command를 등록하면 TC Pre-Command가 초기화됩니다.\n\n계속하시겠습니까?');
				if (!ok) return;
			}
			await assignSlots(item.id, locs);
			toast.success(`${selectedSlots.length}개 슬롯에 "${item.name}" 등록`);
			onAssignmentChanged?.();
		} catch (e: any) {
			toast.error('등록 실패');
		}
	}

	async function handleUnassign() {
		if (selectedSlots.length === 0) return;
		const locs = selectedSlots.map(s => s.setLocation).filter(l => !!l);
		if (locs.length === 0) return;
		try {
			await unassignSlots(locs);
			toast.success('Pre-Command 해제 완료');
			onAssignmentChanged?.();
		} catch (e: any) {
			toast.error('해제 실패');
		}
	}

	// ── 즉시 실행 ──

	function handleExecute(item: PreCommand) {
		onExecute?.(item);
		open = false;
	}

	// ── CRUD (관리 모드) ──

	function startCreate() {
		editingId = null;
		editName = '';
		editDescription = '';
		editCommands = '';
		mode = 'edit';
	}

	function startEdit(item: PreCommand) {
		editingId = item.id;
		editName = item.name;
		editDescription = item.description || '';
		try {
			const cmds: string[] = JSON.parse(item.commands);
			editCommands = cmds.join('\n');
		} catch {
			editCommands = item.commands;
		}
		mode = 'edit';
	}

	async function handleSave() {
		if (!editName.trim()) { toast.error('이름을 입력해주세요'); return; }
		const lines = editCommands.split('\n').filter((l) => l.trim());
		if (lines.length === 0) { toast.error('명령어를 입력해주세요'); return; }

		saving = true;
		try {
			const commandsJson = JSON.stringify(lines);
			if (editingId) {
				await updatePreCommand(editingId, { name: editName.trim(), description: editDescription.trim() || undefined, commands: commandsJson });
				toast.success('수정되었습니다');
			} else {
				await createPreCommand({ name: editName.trim(), description: editDescription.trim() || undefined, commands: commandsJson });
				toast.success('생성되었습니다');
			}
			await loadItems();
			mode = 'manage';
		} catch (e: any) {
			toast.error('저장 실패: ' + (e.message || ''));
		} finally {
			saving = false;
		}
	}

	async function handleDelete(item: PreCommand) {
		if (!confirm(`"${item.name}"을(를) 삭제하시겠습니까?`)) return;
		try {
			await deletePreCommand(item.id);
			toast.success('삭제되었습니다');
			await loadItems();
		} catch { toast.error('삭제 실패'); }
	}

	function parseCommands(json: string): string[] {
		try { return JSON.parse(json); }
		catch { return [json]; }
	}

	function slotLabels(): string {
		if (selectedSlots.length <= 3) return selectedSlots.map(s => s.slotLabel).join(', ');
		return selectedSlots.slice(0, 2).map(s => s.slotLabel).join(', ') + ` 외 ${selectedSlots.length - 2}개`;
	}
</script>

<Sheet.Root bind:open>
	<Sheet.Content side="right" class="w-[420px] flex flex-col max-h-[100dvh]">
		{#if mode === 'main'}
			<!-- ═══ 통합 뷰 ═══ -->
			<Sheet.Header class="pb-3">
				<Sheet.Title class="flex items-center gap-2">
					<ZapIcon class="size-4" />
					Pre-Command
				</Sheet.Title>
				<Sheet.Description class="text-xs">
					{#if selectedSlots.length > 0}
						<span class="font-medium text-foreground">{slotLabels()}</span> 선택됨
						{#if assignedCommandId}
							{@const name = items.find(i => i.id === assignedCommandId)?.name}
							· 현재 등록: <span class="font-medium text-primary">{name}</span>
						{:else if hasMixedAssignment}
							· <span class="text-amber-500">슬롯별 등록이 다름</span>
						{/if}
					{:else}
						슬롯을 선택한 후 Pre-Command를 관리하세요
					{/if}
				</Sheet.Description>
			</Sheet.Header>

			<div class="flex-1 overflow-y-auto min-h-0 space-y-1.5 px-1">
				{#if loading}
					<div class="flex items-center justify-center py-12 text-muted-foreground">
						<LoaderIcon class="size-5 animate-spin" />
					</div>
				{:else if items.length === 0}
					<div class="py-8 space-y-4 px-2">
						<div class="text-center text-muted-foreground text-sm">등록된 명령어가 없습니다</div>
						<div class="rounded-lg border border-dashed p-3 space-y-2">
							<div class="text-xs font-medium text-muted-foreground">예시: tiotest 설치</div>
							<div class="space-y-0.5">
								<div class="text-[10px] font-mono text-muted-foreground/70 bg-muted/30 rounded px-1.5 py-0.5">adb push tiotest-0.52 /dev</div>
								<div class="text-[10px] font-mono text-muted-foreground/70 bg-muted/30 rounded px-1.5 py-0.5">adb shell chmod +x /dev/tiotest-0.52</div>
							</div>
							<div class="text-[10px] text-muted-foreground/60">adb 명령어는 자동으로 -s usbId가 추가됩니다</div>
						</div>
						<button
							class="w-full px-4 py-2 text-sm rounded-md bg-primary text-primary-foreground hover:bg-primary/90 transition-colors"
							onclick={() => { mode = 'manage'; startCreate(); }}
						>
							첫 명령어 만들기
						</button>
					</div>
				{:else}
					{#each items as item (item.id)}
						{@const isAssigned = assignedCommandId === item.id}
						<div
							class="rounded-lg border transition-all duration-150 {isAssigned ? 'border-primary/50 bg-primary/5' : 'hover:border-primary/20 hover:bg-accent/30'}"
						>
							<!-- 카드 본문 -->
							<div class="p-3 pb-2">
								<div class="flex items-center gap-2 mb-1">
									{#if isAssigned}
										<span class="flex items-center justify-center size-5 rounded-full bg-primary text-primary-foreground shrink-0">
											<CheckIcon class="size-3" />
										</span>
									{:else}
										<span class="flex items-center justify-center size-5 rounded-full border border-muted-foreground/30 shrink-0">
											<ZapIcon class="size-3 text-muted-foreground/50" />
										</span>
									{/if}
									<div class="min-w-0 flex-1">
										<div class="font-medium text-sm truncate {isAssigned ? 'text-primary' : ''}">{item.name}</div>
										{#if item.description}
											<div class="text-[11px] text-muted-foreground truncate">{item.description}</div>
										{/if}
									</div>
								</div>
								<div class="ml-7 space-y-0.5">
									{#each parseCommands(item.commands) as cmd}
										<div class="text-[10px] font-mono text-muted-foreground bg-muted/50 rounded px-1.5 py-0.5 truncate">{cmd}</div>
									{/each}
								</div>
							</div>

							<!-- 액션 버튼 -->
							{#if selectedSlots.length > 0}
								<div class="flex items-center gap-1.5 px-3 pb-2.5 ml-7">
									{#if isAssigned}
										<button
											class="px-2.5 py-1 text-[11px] rounded-md border border-muted-foreground/20 text-muted-foreground hover:bg-accent transition-colors"
											onclick={() => handleUnassign()}
										>
											해제
										</button>
									{:else}
										<button
											class="px-2.5 py-1 text-[11px] rounded-md bg-primary text-primary-foreground hover:bg-primary/90 transition-colors"
											onclick={() => handleAssign(item)}
										>
											{assignedCommandId ? '변경' : '등록'}
										</button>
									{/if}
									<button
										class="px-2.5 py-1 text-[11px] rounded-md border hover:bg-accent transition-colors inline-flex items-center gap-1"
										onclick={() => handleExecute(item)}
									>
										<PlayIcon class="size-3" />
										즉시 실행
									</button>
								</div>
							{/if}
						</div>
					{/each}
				{/if}
			</div>

			<!-- 하단 -->
			<div class="shrink-0 pt-3 border-t flex items-center justify-between">
				<button
					class="px-3 py-1.5 text-xs rounded-md hover:bg-accent transition-colors inline-flex items-center gap-1.5 text-muted-foreground hover:text-foreground"
					onclick={() => { mode = 'manage'; }}
				>
					<SettingsIcon class="size-3.5" />
					관리
				</button>
				{#if assignedCommandId && selectedSlots.length > 0}
					<button
						class="px-3 py-1.5 text-xs rounded-md text-destructive hover:bg-destructive/10 transition-colors"
						onclick={() => handleUnassign()}
					>
						해제
					</button>
				{/if}
			</div>

		{:else if mode === 'manage'}
			<!-- ═══ 관리 뷰 (편집/삭제) ═══ -->
			<Sheet.Header class="pb-3">
				<Sheet.Title class="flex items-center gap-2">
					<button
						class="p-0.5 rounded hover:bg-accent transition-colors"
						onclick={() => { mode = 'main'; }}
					>
						<ChevronLeftIcon class="size-4" />
					</button>
					명령어 관리
				</Sheet.Title>
				<Sheet.Description class="text-xs">편집, 삭제, 새로 만들기</Sheet.Description>
			</Sheet.Header>

			<div class="flex-1 overflow-y-auto min-h-0 space-y-2 px-1">
				{#each items as item (item.id)}
					<div class="p-3 rounded-lg border group transition-all duration-150 hover:bg-accent/30 hover:border-primary/20">
						<div class="flex items-start justify-between gap-2">
							<div class="min-w-0 flex-1">
								<div class="font-medium text-sm truncate">{item.name}</div>
								{#if item.description}
									<div class="text-xs text-muted-foreground mt-0.5 truncate">{item.description}</div>
								{/if}
								<div class="mt-1.5 space-y-0.5">
									{#each parseCommands(item.commands) as cmd}
										<div class="text-[10px] font-mono text-muted-foreground bg-muted/50 rounded px-1.5 py-0.5 truncate">{cmd}</div>
									{/each}
								</div>
							</div>
							<div class="flex items-center gap-1 opacity-0 group-hover:opacity-100 transition-opacity shrink-0">
								<button
									class="p-1.5 rounded-md hover:bg-accent transition-colors"
									onclick={() => startEdit(item)}
									title="편집"
								>
									<PencilIcon class="size-3.5" />
								</button>
								<button
									class="p-1.5 rounded-md hover:bg-destructive/10 text-destructive transition-colors"
									onclick={() => handleDelete(item)}
									title="삭제"
								>
									<TrashIcon class="size-3.5" />
								</button>
							</div>
						</div>
					</div>
				{/each}
			</div>

			<div class="shrink-0 pt-3 border-t">
				<button
					class="w-full px-4 py-2 text-sm rounded-md border border-dashed transition-all duration-150 flex items-center justify-center gap-2 text-muted-foreground hover:text-foreground hover:bg-accent/50 hover:border-primary/30 active:scale-[0.99]"
					onclick={startCreate}
				>
					<PlusIcon class="size-4" />
					새 명령어 추가
				</button>
			</div>

		{:else}
			<!-- ═══ 편집 뷰 ═══ -->
			<Sheet.Header class="pb-3">
				<Sheet.Title class="flex items-center gap-2">
					<button
						class="p-0.5 rounded hover:bg-accent transition-colors"
						onclick={() => { mode = 'manage'; }}
					>
						<ChevronLeftIcon class="size-4" />
					</button>
					{editingId ? '명령어 편집' : '새 명령어'}
				</Sheet.Title>
				<Sheet.Description class="text-xs">
					adb 명령어는 실행 시 자동으로 -s {'{usbId}'} 가 삽입됩니다
				</Sheet.Description>
			</Sheet.Header>

			<div class="flex-1 overflow-y-auto min-h-0 space-y-4 px-1">
				<div>
					<label for="pc-name" class="text-xs font-medium text-muted-foreground mb-1 block">이름</label>
					<input
						id="pc-name"
						type="text"
						class="w-full px-3 py-2 text-sm rounded-md border bg-background focus:outline-none focus:ring-1 focus:ring-ring"
						placeholder="예: tiotest 설치"
						bind:value={editName}
					/>
				</div>
				<div>
					<label for="pc-desc" class="text-xs font-medium text-muted-foreground mb-1 block">설명 <span class="text-muted-foreground/60">(선택)</span></label>
					<input
						id="pc-desc"
						type="text"
						class="w-full px-3 py-2 text-sm rounded-md border bg-background focus:outline-none focus:ring-1 focus:ring-ring"
						placeholder="설명을 입력하세요"
						bind:value={editDescription}
					/>
				</div>
				<div>
					<label for="pc-cmds" class="text-xs font-medium text-muted-foreground mb-1 block">
						명령어 <span class="text-muted-foreground/60">(줄 단위)</span>
					</label>
					<textarea
						id="pc-cmds"
						class="w-full px-3 py-2 text-sm rounded-md border bg-background focus:outline-none focus:ring-1 focus:ring-ring font-mono leading-relaxed resize-none"
						rows={8}
						placeholder={"adb push tiotest-0.52 /dev\nadb shell chmod +x /dev/tiotest-0.52"}
						bind:value={editCommands}
					></textarea>
					<div class="text-[11px] text-muted-foreground mt-1">
						한 줄에 하나의 명령어. adb 명령어는 자동으로 <code class="bg-muted px-1 rounded">-s usbId</code> 추가
					</div>
				</div>
			</div>

			<div class="shrink-0 pt-3 border-t flex gap-2">
				<button
					class="flex-1 px-4 py-2 text-sm rounded-md border hover:bg-accent transition-colors"
					onclick={() => { mode = 'manage'; }}
				>취소</button>
				<button
					class="flex-1 px-4 py-2 text-sm rounded-md bg-primary text-primary-foreground hover:bg-primary/90 transition-colors disabled:opacity-50 inline-flex items-center justify-center gap-2"
					disabled={saving}
					onclick={handleSave}
				>
					{#if saving}<LoaderIcon class="size-4 animate-spin" />{:else}<SaveIcon class="size-4" />{/if}
					저장
				</button>
			</div>
		{/if}
	</Sheet.Content>
</Sheet.Root>
