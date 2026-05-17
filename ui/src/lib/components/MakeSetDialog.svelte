<script lang="ts">
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import LogBrowserDialog from './LogBrowserDialog.svelte';
	import MakeSetGroupManager from './MakeSetGroupManager.svelte';
	import {
		sendHeadCommand,
		fetchDdOptions,
		fetchMakesetGroups,
		type DdOption,
		type MakesetGroup
	} from '$lib/api/testdb.js';
	import type { HeadSlotData } from '$lib/api/headSlotStore.svelte.js';
	import LoaderIcon from '@lucide/svelte/icons/loader-circle';
	import FolderOpenIcon from '@lucide/svelte/icons/folder-open';
	import SettingsIcon from '@lucide/svelte/icons/settings';
	import AlertTriangleIcon from '@lucide/svelte/icons/triangle-alert';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		slots: { slotNumber: number; headData: HeadSlotData }[];
		source: string;
		onClose: () => void;
	}

	let { open = $bindable(), slots, source, onClose }: Props = $props();

	// Mode
	const isMulti = $derived(slots.length > 1);

	// Checkbox state (single mode)
	let fwEnabled = $state(true);
	let provEnabled = $state(false);
	let imageEnabled = $state(false);

	// FW options — common (일괄)
	let fwMode = $state('0x1');
	let fwPath = $state('');

	// Multi-slot FW mode: 'common' (일괄) or 'individual' (개별)
	let fwAssignMode = $state<'common' | 'individual'>('common');

	// Per-slot FW paths (slotNumber → fwPath)
	let slotFwPaths = $state<Record<number, string>>({});

	// Provision options (single mode)
	let provPath = $state('');

	// Image options (single mode)
	let imagePath = $state('');
	let ddOptions = $state<DdOption[]>([]);
	let ddValue = $state('none');
	let ddLoading = $state(false);

	// LogBrowserDialog sub-dialog state
	let browseOpen = $state(false);
	let browseTentacle = $state('');
	let browsePath = $state('');
	let browseTitle = $state('');
	let browseSelectMode = $state(false);
	let browseFolderSelect = $state(false);
	let browseFileFilter: ((entry: { name: string; directory: boolean; size: number; lastModified: number }) => boolean) | undefined = $state(undefined);
	let browseTarget = $state<'fw' | 'provision' | 'image' | `fw-slot-${number}`>('fw');

	let uploading = $state(false);

	// Multi-slot mode: group state
	let makesetGroups = $state<MakesetGroup[]>([]);
	let selectedGroupId = $state<number | null>(null);
	let groupManagerOpen = $state(false);

	// Derived product info from first slot
	const board = $derived(slots[0]?.headData?.board ?? '');
	const controller = $derived(slots[0]?.headData?.controller ?? '');
	const nandType = $derived(slots[0]?.headData?.nandType ?? '');
	const cellType = $derived(slots[0]?.headData?.cellType ?? '');
	const nandSize = $derived(slots[0]?.headData?.nandSize ?? '');
	const density = $derived(slots[0]?.headData?.density ?? '');

	// Multi-mode: board matching result
	interface BoardMatch {
		board: string;
		provisionPath: string;
		imagePath: string;
		ddValue: string;
		slotNumbers: number[];
	}

	const selectedGroup = $derived(makesetGroups.find(g => g.id === selectedGroupId) ?? null);

	const boardMatches = $derived.by(() => {
		if (!selectedGroup) return { matched: [] as BoardMatch[], skipped: [] as { slotNumber: number; board: string }[] };

		const matched: BoardMatch[] = [];
		const skipped: { slotNumber: number; board: string }[] = [];

		const boardSlotMap = new Map<string, number[]>();
		for (const s of slots) {
			const b = s.headData?.board ?? '';
			if (!boardSlotMap.has(b)) boardSlotMap.set(b, []);
			boardSlotMap.get(b)!.push(s.slotNumber);
		}

		for (const [b, slotNums] of boardSlotMap) {
			const groupItem = selectedGroup.items.find(i => i.board === b);
			if (groupItem) {
				matched.push({
					board: b,
					provisionPath: groupItem.provisionPath,
					imagePath: groupItem.imagePath,
					ddValue: groupItem.ddValue,
					slotNumbers: slotNums
				});
			} else {
				for (const sn of slotNums) {
					skipped.push({ slotNumber: sn, board: b });
				}
			}
		}

		return { matched, skipped };
	});

	// Load when dialog opens — open 이 false→true 전환 시 1회만 실행.
	// 단순 `if (open)` 으로 두면 dialog 가 열린 동안 reactive state 변화로
	// effect 가 재실행될 때마다 resetState + fetch 가 반복되어 깜빡임 발생.
	let lastOpenDialog = false;
	$effect(() => {
		if (open && !lastOpenDialog) {
			resetState();
			if (!isMulti && board) {
				loadDdOptions();
			}
			loadGroups();
		}
		lastOpenDialog = open;
	});

	function resetState() {
		fwEnabled = true;
		provEnabled = false;
		imageEnabled = false;
		fwMode = '0x1';
		fwPath = '';
		fwAssignMode = 'common';
		slotFwPaths = {};
		provPath = '';
		imagePath = '';
		ddValue = 'none';
		selectedGroupId = null;
	}

	async function loadGroups() {
		try {
			makesetGroups = await fetchMakesetGroups();
		} catch (e) {
			console.error('Failed to load makeset groups:', e);
		}
	}

	async function loadDdOptions() {
		ddLoading = true;
		try {
			ddOptions = await fetchDdOptions('HEAD', board);
			ddValue = ddOptions.find(d => d.name === 'none') ? 'none' : (ddOptions[0]?.name ?? 'none');
		} catch (e) {
			console.error('Failed to load DD options:', e);
			ddOptions = [{ name: 'none', enabled: true }];
			ddValue = 'none';
		} finally {
			ddLoading = false;
		}
	}

	const FW_PREFIX = '/home/octo/FW';
	const ONEDOWN_PREFIX = '/home/octo/testboard/onedown';
	const QUAL_IMAGE_PREFIX = '/home/octo/testboard/qual_image';

	function getFwBrowsePath() {
		return `${FW_PREFIX}/${controller}/${nandType}/${cellType}/${nandSize}/${density}`;
	}

	function getFwBrowseFallbackPath() {
		return `${FW_PREFIX}/${controller}/${nandType}/${cellType}/${nandSize}`;
	}

	function stripPrefix(path: string, prefix: string): string {
		if (!path) return path;
		if (path.startsWith(prefix + '/')) return path.slice(prefix.length + 1);
		if (path === prefix) return '';
		return path;
	}

	function stripFwPrefix(path: string): string {
		return stripPrefix(path, FW_PREFIX);
	}

	function stripProvPrefix(path: string): string {
		return stripPrefix(path, ONEDOWN_PREFIX);
	}

	function stripImagePrefix(path: string): string {
		return stripPrefix(path, QUAL_IMAGE_PREFIX);
	}

	async function pathExists(tentacle: string, path: string): Promise<boolean> {
		try {
			const params = new URLSearchParams({ tentacleName: tentacle, path });
			const res = await fetch(`/api/log-browser/files?${params}`);
			return res.ok;
		} catch {
			return false;
		}
	}

	async function resolveFwBrowsePath(): Promise<string> {
		const primary = getFwBrowsePath();
		if (await pathExists('HEAD', primary)) return primary;
		const fallback = getFwBrowseFallbackPath();
		if (await pathExists('HEAD', fallback)) {
			toast.warning(`FW 경로에 density(${density || '-'}) 폴더가 없어 nandsize 경로로 이동합니다`);
			return fallback;
		}
		return primary;
	}

	async function openFwBrowse() {
		browseTentacle = 'HEAD';
		browsePath = await resolveFwBrowsePath();
		browseTitle = 'Select FW Binary';
		browseSelectMode = true;
		browseFolderSelect = false;
		browseFileFilter = undefined;
		browseTarget = 'fw';
		browseOpen = true;
	}

	async function openSlotFwBrowse(slotNumber: number) {
		browseTentacle = 'HEAD';
		browsePath = await resolveFwBrowsePath();
		browseTitle = `Select FW — Slot ${slotNumber}`;
		browseSelectMode = true;
		browseFolderSelect = false;
		browseFileFilter = undefined;
		browseTarget = `fw-slot-${slotNumber}`;
		browseOpen = true;
	}

	function openProvBrowse() {
		const prefix = '/home/octo/testboard/onedown';
		const lower = board.toLowerCase();
		// 보드 plat 결정:
		// - sm... : qct (Qualcomm)
		// - erd...: lsi (Samsung LSI) — 뒤 "_xxx" 접미사는 무시하고 erdNNNN 부분만 폴더명으로 사용
		// - 그 외 : mtk (MediaTek)
		let boardSuffix: string;
		let boardDir: string;
		if (lower.startsWith('sm')) {
			boardSuffix = 'qct';
			boardDir = board;
		} else if (lower.startsWith('erd')) {
			boardSuffix = 'lsi';
			const underscoreIdx = board.indexOf('_');
			boardDir = underscoreIdx > 0 ? board.substring(0, underscoreIdx) : board;
		} else {
			boardSuffix = 'mtk';
			boardDir = board;
		}
		const path = `${prefix}/${boardSuffix}/${boardDir}/provision`;

		browseTentacle = 'HEAD';
		browsePath = path;
		browseTitle = 'Select Provision XML';
		browseSelectMode = true;
		browseFolderSelect = false;
		browseFileFilter = (entry) => entry.name.toLowerCase().endsWith('.xml');
		browseTarget = 'provision';
		browseOpen = true;
	}

	function openImageBrowse() {
		const path = `/home/octo/testboard/qual_image/${board}/1.image`;

		browseTentacle = 'HEAD';
		browsePath = path;
		browseTitle = 'Select Image Folder';
		browseSelectMode = false;
		browseFolderSelect = true;
		browseFileFilter = undefined;
		browseTarget = 'image';
		browseOpen = true;
	}

	function handleBrowseSelect(filePath: string) {
		if (browseTarget === 'fw') {
			fwPath = filePath;
		} else if (browseTarget === 'provision') {
			provPath = filePath;
		} else if (browseTarget === 'image') {
			imagePath = filePath;
		} else if (typeof browseTarget === 'string' && browseTarget.startsWith('fw-slot-')) {
			const slotNum = parseInt(browseTarget.replace('fw-slot-', ''));
			slotFwPaths = { ...slotFwPaths, [slotNum]: filePath };
		}
	}

	// Apply common FW path to all individual slots
	function applyCommonToAll() {
		if (!fwPath) return;
		const updated: Record<number, string> = {};
		for (const s of slots) {
			updated[s.slotNumber] = fwPath;
		}
		slotFwPaths = updated;
	}

	// Single-slot upload
	async function handleUploadSingle() {
		if (!fwEnabled && !provEnabled && !imageEnabled) {
			toast.error('하나 이상 선택하세요');
			return;
		}
		if (fwEnabled && !fwPath) {
			toast.error('FW 경로를 지정하세요');
			return;
		}
		if (provEnabled && !provPath) {
			toast.error('Provision 경로를 지정하세요');
			return;
		}
		if (imageEnabled && !imagePath) {
			toast.error('Image 경로를 지정하세요');
			return;
		}

		// HEAD makeset 프로토콜: 5 키워드(fwbin/mode/provision/image/dd) 무조건 포함, 값이 비어도 "key;" 만.
		// enable 체크박스는 사용자 입력 검증용 뿐이고 메시지 토큰 구성과는 무관.
		const fwbinVal = fwEnabled && fwPath ? stripFwPrefix(fwPath) : '';
		const modeVal = fwEnabled && fwPath ? fwMode : '';
		const provVal = provEnabled && provPath ? stripProvPrefix(provPath) : '';
		const imageVal = imageEnabled && imagePath ? stripImagePrefix(imagePath) : '';
		const ddVal = imageEnabled && imagePath ? ddValue : '';
		const parts: string[] = [
			`fwbin;${fwbinVal}`,
			`mode;${modeVal}`,
			`provision;${provVal}`,
			`image;${imageVal}`,
			`dd;${ddVal}`
		];

		const slotNumbers = slots.map(s => s.slotNumber);
		uploading = true;
		try {
			await sendHeadCommand({
				source,
				command: 'makeset',
				slotNumbers,
				data: parts.join(',')
			});
			open = false;
			onClose();
		} catch (e: any) {
			toast.error(e instanceof Error ? e.message : 'Upload에 실패했습니다.');
		} finally {
			uploading = false;
		}
	}

	// Multi-slot upload
	async function handleUploadMulti() {
		if (!selectedGroup) {
			toast.error('MakeSet 그룹을 선택하세요');
			return;
		}
		const { matched, skipped } = boardMatches;
		if (matched.length === 0) {
			toast.error('매칭되는 보드가 없습니다');
			return;
		}

		// FW validation
		if (fwEnabled) {
			if (fwAssignMode === 'common' && !fwPath) {
				toast.error('FW 경로를 지정하세요');
				return;
			}
			if (fwAssignMode === 'individual') {
				const allSlotNums = matched.flatMap(m => m.slotNumbers);
				const missing = allSlotNums.filter(sn => !slotFwPaths[sn]);
				if (missing.length > 0) {
					toast.error(`슬롯 ${missing.join(', ')}의 FW 경로를 지정하세요`);
					return;
				}
			}
		}

		if (!fwEnabled && matched.every(m => !m.provisionPath && !m.imagePath)) {
			toast.error('FW를 활성화하거나 그룹에 provision/image 경로를 설정하세요');
			return;
		}

		uploading = true;
		try {
			const promises: Promise<any>[] = [];
			let appliedSlots = 0;
			const emptyBoards: string[] = [];

			if (fwEnabled && fwAssignMode === 'individual') {
				// Individual FW: send per-slot (or group same-fw slots together)
				for (const m of matched) {
					// Group slots within this board match by their FW path
					const fwGroups = new Map<string, number[]>();
					for (const sn of m.slotNumbers) {
						const fw = slotFwPaths[sn] || '';
						if (!fwGroups.has(fw)) fwGroups.set(fw, []);
						fwGroups.get(fw)!.push(sn);
					}
					for (const [fw, sns] of fwGroups) {
						// 5 키워드 항상 포함. 값 없으면 "key;" 만.
						const fwbinVal = fw ? stripFwPrefix(fw) : '';
						const modeVal = fw ? fwMode : '';
						const provVal = m.provisionPath ? stripProvPrefix(m.provisionPath) : '';
						const imageVal = m.imagePath ? stripImagePrefix(m.imagePath) : '';
						const ddVal = m.imagePath ? (m.ddValue || '') : '';
						const parts = [
							`fwbin;${fwbinVal}`,
							`mode;${modeVal}`,
							`provision;${provVal}`,
							`image;${imageVal}`,
							`dd;${ddVal}`
						];
						const hasAnyValue = fwbinVal || provVal || imageVal;
						if (hasAnyValue) {
							promises.push(sendHeadCommand({
								source,
								command: 'makeset',
								slotNumbers: sns,
								data: parts.join(',')
							}));
							appliedSlots += sns.length;
						} else {
							emptyBoards.push(`${m.board}(슬롯 ${sns.join(',')})`);
						}
					}
				}
			} else {
				// Common FW or FW disabled: send per-board — 5 키워드 항상 포함, 값 없으면 "key;" 만.
				for (const m of matched) {
					const fwbinVal = fwEnabled && fwPath ? stripFwPrefix(fwPath) : '';
					const modeVal = fwEnabled && fwPath ? fwMode : '';
					const provVal = m.provisionPath ? stripProvPrefix(m.provisionPath) : '';
					const imageVal = m.imagePath ? stripImagePrefix(m.imagePath) : '';
					const ddVal = m.imagePath ? (m.ddValue || '') : '';
					const parts = [
						`fwbin;${fwbinVal}`,
						`mode;${modeVal}`,
						`provision;${provVal}`,
						`image;${imageVal}`,
						`dd;${ddVal}`
					];
					const hasAnyValue = fwbinVal || provVal || imageVal;
					if (hasAnyValue) {
						promises.push(sendHeadCommand({
							source,
							command: 'makeset',
							slotNumbers: m.slotNumbers,
							data: parts.join(',')
						}));
						appliedSlots += m.slotNumbers.length;
					} else {
						emptyBoards.push(`${m.board}(슬롯 ${m.slotNumbers.join(',')})`);
					}
				}
			}

			if (promises.length === 0) {
				toast.error('적용할 항목이 없습니다 (FW/Provision/Image 모두 비어있음)');
				uploading = false;
				return;
			}

			await Promise.all(promises);

			const skippedCount = skipped.length;
			const messages: string[] = [`${appliedSlots}개 슬롯 적용 완료`];
			if (skippedCount > 0) {
				messages.push(`${skippedCount}개 슬롯 스킵 (그룹 미매칭)`);
			}
			if (emptyBoards.length > 0) {
				messages.push(`${emptyBoards.length}개 보드 스킵 (설정 비어있음): ${emptyBoards.join(', ')}`);
			}
			toast.success(messages.join(' · '));
			open = false;
			onClose();
		} catch (e: any) {
			toast.error(e instanceof Error ? e.message : 'Upload에 실패했습니다.');
		} finally {
			uploading = false;
		}
	}

	function handleUpload() {
		if (isMulti) {
			handleUploadMulti();
		} else {
			handleUploadSingle();
		}
	}

	function displayPath(path: string): string {
		if (!path) return '(선택 안됨)';
		return path.split('/').pop() || path;
	}

	function getSlotLabel(slotNumber: number): string {
		const s = slots.find(s => s.slotNumber === slotNumber);
		return s?.headData?.setLocation ?? `#${slotNumber}`;
	}
</script>

<Dialog.Root bind:open onOpenChange={(v) => { if (!v) onClose(); }}>
	<Dialog.Content class="sm:max-w-[90vw] lg:max-w-2xl max-h-[85vh] overflow-y-auto">
		<Dialog.Header>
			<Dialog.Title class="text-sm font-semibold">
				{#if isMulti}
					MakeSet — 다중 슬롯 ({slots.length}개 선택)
				{:else}
					MakeSet — {board}
				{/if}
			</Dialog.Title>
			<Dialog.Description>
				<span class="text-xs text-muted-foreground">
					{slots.length}개 슬롯 선택됨 · {controller}
				</span>
			</Dialog.Description>
		</Dialog.Header>

		<div class="space-y-4 py-2">
			{#if isMulti}
				<!-- Multi-slot mode: Group selector -->
				<div class="border rounded-md p-3 space-y-2">
					<div class="flex items-center gap-2">
						<span class="text-sm font-medium">MakeSet Group:</span>
						<select
							bind:value={selectedGroupId}
							class="flex-1 border rounded px-2 py-1 text-xs bg-background"
						>
							<option value={null}>-- 그룹 선택 --</option>
							{#each makesetGroups as group (group.id)}
								<option value={group.id}>{group.name} ({group.items.length}개 보드)</option>
							{/each}
						</select>
						<button
							class="px-2 py-1 rounded border text-xs hover:bg-muted transition-colors flex items-center gap-1"
							onclick={() => { groupManagerOpen = true; }}
							title="그룹 관리"
						>
							<SettingsIcon class="size-3" />
							관리
						</button>
					</div>
				</div>
			{/if}

			<!-- FW Section -->
			<div class="border rounded-md p-3 space-y-2">
				<label class="flex items-center gap-2 cursor-pointer">
					<input type="checkbox" bind:checked={fwEnabled} class="accent-blue-600" />
					<span class="text-sm font-medium">FW</span>
				</label>
				{#if fwEnabled}
					<div class="ml-6 space-y-2">
						<!-- Mode radio -->
						<div class="flex items-center gap-3 text-xs">
							<span class="text-muted-foreground w-10">Mode:</span>
							<label class="flex items-center gap-1 cursor-pointer">
								<input type="radio" name="fwMode" value="0x1" bind:group={fwMode} />
								0x1
							</label>
							<label class="flex items-center gap-1 cursor-pointer">
								<input type="radio" name="fwMode" value="0xe" bind:group={fwMode} />
								0xe
							</label>
						</div>

						{#if isMulti}
							<!-- Multi: assign mode toggle -->
							<div class="flex items-center gap-3 text-xs">
								<span class="text-muted-foreground w-10">할당:</span>
								<label class="flex items-center gap-1 cursor-pointer">
									<input type="radio" name="fwAssign" value="common" bind:group={fwAssignMode} />
									일괄
								</label>
								<label class="flex items-center gap-1 cursor-pointer">
									<input type="radio" name="fwAssign" value="individual" bind:group={fwAssignMode} />
									개별
								</label>
							</div>
						{/if}

						{#if !isMulti || fwAssignMode === 'common'}
							<!-- Common FW path -->
							<div class="flex items-center gap-2 text-xs">
								<span class="text-muted-foreground w-10">Path:</span>
								<span class="flex-1 truncate font-mono text-[10px] text-foreground/70" title={fwPath}>
									{displayPath(fwPath)}
								</span>
								<button
									class="px-2 py-1 rounded border text-xs hover:bg-muted transition-colors flex items-center gap-1"
									onclick={openFwBrowse}
								>
									<FolderOpenIcon class="size-3" />
									Browse
								</button>
							</div>
						{:else}
							<!-- Individual FW per slot -->
							<div class="space-y-1">
								<div class="flex items-center gap-2 text-xs mb-1">
									<span class="text-muted-foreground">공통 경로로 일괄 적용:</span>
									<span class="flex-1 truncate font-mono text-[10px] text-foreground/70" title={fwPath}>
										{displayPath(fwPath)}
									</span>
									<button
										class="px-1.5 py-0.5 rounded border text-[10px] hover:bg-muted transition-colors flex items-center gap-1"
										onclick={openFwBrowse}
									>
										<FolderOpenIcon class="size-2.5" />
										Browse
									</button>
									<button
										class="px-1.5 py-0.5 rounded bg-blue-600 text-white text-[10px] hover:bg-blue-700 transition-colors disabled:opacity-50"
										onclick={applyCommonToAll}
										disabled={!fwPath}
									>
										전체 적용
									</button>
								</div>
								<div class="border rounded overflow-hidden">
									<table class="w-full text-xs">
										<thead>
											<tr class="border-b bg-muted/30">
												<th class="px-2 py-1 text-left font-medium w-20">Slot</th>
												<th class="px-2 py-1 text-left font-medium">FW Path</th>
												<th class="px-2 py-1 w-16"></th>
											</tr>
										</thead>
										<tbody>
											{#each slots as s (s.slotNumber)}
												<tr class="border-b last:border-b-0">
													<td class="px-2 py-1 font-medium">{getSlotLabel(s.slotNumber)}</td>
													<td class="px-2 py-1 font-mono text-[10px] text-foreground/70 truncate max-w-[200px]" title={slotFwPaths[s.slotNumber] ?? ''}>
														{slotFwPaths[s.slotNumber] ? displayPath(slotFwPaths[s.slotNumber]) : '(선택 안됨)'}
													</td>
													<td class="px-1 py-1 text-right">
														<button
															class="px-1.5 py-0.5 rounded border text-[10px] hover:bg-muted transition-colors inline-flex items-center gap-0.5"
															onclick={() => openSlotFwBrowse(s.slotNumber)}
														>
															<FolderOpenIcon class="size-2.5" />
															Browse
														</button>
													</td>
												</tr>
											{/each}
										</tbody>
									</table>
								</div>
							</div>
						{/if}
					</div>
				{/if}
			</div>

			{#if isMulti}
				<!-- Multi-slot mode: Board match table -->
				{#if selectedGroup}
					<div class="border rounded-md overflow-hidden">
						<div class="px-3 py-1.5 bg-muted/50 text-xs font-medium border-b">
							보드별 설정 (그룹에서 로드)
						</div>
						{#if boardMatches.matched.length > 0}
							<table class="w-full text-xs">
								<thead>
									<tr class="border-b bg-muted/30">
										<th class="px-2 py-1 text-left font-medium">Board</th>
										<th class="px-2 py-1 text-left font-medium">Provision</th>
										<th class="px-2 py-1 text-left font-medium">Image</th>
										<th class="px-2 py-1 text-left font-medium w-14">DD</th>
										<th class="px-2 py-1 text-left font-medium w-16">Slots</th>
									</tr>
								</thead>
								<tbody>
									{#each boardMatches.matched as match}
										<tr class="border-b last:border-b-0">
											<td class="px-2 py-1 font-medium">{match.board}</td>
											<td class="px-2 py-1 font-mono text-[10px] text-foreground/70 truncate max-w-[140px]" title={match.provisionPath}>
												{match.provisionPath ? displayPath(match.provisionPath) : '-'}
											</td>
											<td class="px-2 py-1 font-mono text-[10px] text-foreground/70 truncate max-w-[140px]" title={match.imagePath}>
												{match.imagePath ? displayPath(match.imagePath) : '-'}
											</td>
											<td class="px-2 py-1">{match.ddValue || 'none'}</td>
											<td class="px-2 py-1 font-mono">{match.slotNumbers.join(',')}</td>
										</tr>
									{/each}
								</tbody>
							</table>
						{/if}

						{#if boardMatches.skipped.length > 0}
							<div class="px-3 py-2 bg-yellow-50 border-t flex items-start gap-1.5">
								<AlertTriangleIcon class="size-3.5 text-yellow-600 mt-0.5 shrink-0" />
								<span class="text-xs text-yellow-700">
									{boardMatches.skipped.length}개 슬롯 스킵됨
									({[...new Set(boardMatches.skipped.map(s => s.board))].join(', ')}: 그룹에 없음)
								</span>
							</div>
						{/if}
					</div>
				{/if}
			{:else}
				<!-- Single-slot mode: Provision & Image sections -->
				<div class="border rounded-md p-3 space-y-2">
					<label class="flex items-center gap-2 cursor-pointer">
						<input type="checkbox" bind:checked={provEnabled} class="accent-blue-600" />
						<span class="text-sm font-medium">Provision</span>
					</label>
					{#if provEnabled}
						<div class="ml-6">
							<div class="flex items-center gap-2 text-xs">
								<span class="text-muted-foreground w-10">Path:</span>
								<span class="flex-1 truncate font-mono text-[10px] text-foreground/70" title={provPath}>
									{displayPath(provPath)}
								</span>
								<button
									class="px-2 py-1 rounded border text-xs hover:bg-muted transition-colors flex items-center gap-1"
									onclick={openProvBrowse}
								>
									<FolderOpenIcon class="size-3" />
									Browse
								</button>
							</div>
						</div>
					{/if}
				</div>

				<div class="border rounded-md p-3 space-y-2">
					<label class="flex items-center gap-2 cursor-pointer">
						<input type="checkbox" bind:checked={imageEnabled} class="accent-blue-600" />
						<span class="text-sm font-medium">Image</span>
					</label>
					{#if imageEnabled}
						<div class="ml-6 space-y-2">
							<div class="flex items-center gap-2 text-xs">
								<span class="text-muted-foreground w-10">Path:</span>
								<span class="flex-1 truncate font-mono text-[10px] text-foreground/70" title={imagePath}>
									{displayPath(imagePath)}
								</span>
								<button
									class="px-2 py-1 rounded border text-xs hover:bg-muted transition-colors flex items-center gap-1"
									onclick={openImageBrowse}
								>
									<FolderOpenIcon class="size-3" />
									Browse
								</button>
							</div>
							<div class="flex items-center gap-2 text-xs">
								<span class="text-muted-foreground w-10">auto_dd:</span>
								{#if ddLoading}
									<LoaderIcon class="size-3 animate-spin" />
								{:else}
									<select
										bind:value={ddValue}
										class="border rounded px-2 py-1 text-xs bg-background"
									>
										{#each ddOptions as opt (opt.name)}
											<option value={opt.name} disabled={!opt.enabled}>
												{opt.name}{opt.enabled ? '' : ' (N/A)'}
											</option>
										{/each}
									</select>
								{/if}
							</div>
						</div>
					{/if}
				</div>
			{/if}
		</div>

		<Dialog.Footer>
			<button
				class="px-4 py-2 rounded bg-blue-600 text-white text-sm font-medium hover:bg-blue-700 transition-colors disabled:opacity-50 disabled:cursor-not-allowed flex items-center gap-2"
				onclick={handleUpload}
				disabled={uploading || (isMulti && (!selectedGroup || boardMatches.matched.length === 0))}
			>
				{#if uploading}
					<LoaderIcon class="size-4 animate-spin" />
				{/if}
				Upload
			</button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>

<LogBrowserDialog
	bind:open={browseOpen}
	tentacleName={browseTentacle}
	initialPath={browsePath}
	title={browseTitle}
	selectMode={browseSelectMode}
	folderSelect={browseFolderSelect}
	fileFilter={browseFileFilter}
	onClose={() => { browseOpen = false; }}
	onSelect={handleBrowseSelect}
/>

<MakeSetGroupManager
	bind:open={groupManagerOpen}
	{source}
	onClose={() => { groupManagerOpen = false; }}
	onGroupsChanged={loadGroups}
/>
