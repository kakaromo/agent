<script lang="ts">
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { captionMuted } from '$lib/styles/common.js';
	import { Tabs, TabsContent, TabsList, TabsTrigger } from '$lib/components/ui/tabs/index.js';
	import PerfChart from '$lib/components/perf-chart/PerfChart.svelte';
	import { DataTable } from '$lib/components/data-table';
	import JsonView from '$lib/components/bin-mapper/JsonView.svelte';
	import IostatTableView from './IostatTableView.svelte';
	import SegmentHeatmap from './SegmentHeatmap.svelte';
	import BitmapGridView from './BitmapGridView.svelte';
	import {
		fetchMetadataForProduct,
		fetchMetadataForTr,
		fetchSlotMetadata,
		fetchSlotMetadataStatus,
		fetchMetadataFile,
		fetchSlotMetadataFiles,
		fetchExcludedTypes,
		setExcludedTypes,
		fetchSlotMetadataEnabled,
		setSlotMetadataEnabled,
		fetchSlotInterval,
		setSlotInterval,
		type MetadataType,
		type MetadataEntry,
		type SlotMetadataStatus
	} from '$lib/api/metadata.js';
	import { flattenObject, classifyKeys, applyDelta } from '$lib/utils/flattenJson.js';
	import LoaderIcon from '@lucide/svelte/icons/loader-circle';
	import DatabaseIcon from '@lucide/svelte/icons/database';
	import Download from '@lucide/svelte/icons/download';
	import MaximizeIcon from '@lucide/svelte/icons/maximize-2';
	import MinimizeIcon from '@lucide/svelte/icons/minimize-2';
	import SearchIcon from '@lucide/svelte/icons/search';
	import XIcon from '@lucide/svelte/icons/x';
	import ChevronRightIcon from '@lucide/svelte/icons/chevron-right';
	import type { ColumnDef } from '@tanstack/table-core';
	import { toast } from 'svelte-sonner';

	export interface SlotTarget {
		tentacleName: string;
		slotNumber: number;
		controller?: string;
		nandType?: string;
		cellType?: string;
		fwVer?: string;
		logPath?: string;
		testTrId?: number;
		headType?: number; // 0=compatibility, 1=performance
		tentacleIp?: string;
	}

	interface Props {
		open: boolean;
		tentacleName: string;
		slotNumber: number;
		controller?: string;
		nandType?: string;
		cellType?: string;
		fwVer?: string;
		logPath?: string;
		testTrId?: number;
		headType?: number;
		tentacleIp?: string;
		slots?: SlotTarget[];
		/** 결과(History) 보기 전용 — 모니터링 컨트롤 바 숨김. 평가 끝난 데이터에 모니터링 의미 없음. */
		readOnly?: boolean;
		onClose: () => void;
	}

	let {
		open = $bindable(),
		tentacleName,
		slotNumber,
		controller,
		nandType,
		cellType,
		fwVer,
		logPath,
		testTrId,
		headType,
		tentacleIp,
		slots,
		readOnly = false,
		onClose
	}: Props = $props();

	// 멀티 슬롯 지원: slots가 있으면 사용, 없으면 단일 슬롯
	const allSlots = $derived<SlotTarget[]>(
		slots && slots.length > 0
			? slots
			: [{ tentacleName, slotNumber, controller, nandType, cellType, fwVer, logPath, testTrId, headType, tentacleIp }]
	);

	let activeSlotIdx = $state(0);
	const activeSlot = $derived(allSlots[activeSlotIdx] ?? allSlots[0]);

	// State
	let loading = $state(false);
	let metadataTypes = $state<MetadataType[]>([]);
	let selectedTypeKey = $state<string | null>(null);
	let entries = $state<MetadataEntry[]>([]);
	let loadingData = $state(false);
	let slotStatus = $state<SlotMetadataStatus | null>(null);
	let availableTypeKeys = $state<Set<string>>(new Set());
	let savedFilePaths = $state<Map<string, string>>(new Map()); // typeKey → 실제 파일 full path
	let excludedTypeKeys = $state<Set<string>>(new Set());
	let monitorEnabled = $state(false);
	let monitorToggling = $state(false);
	let intervalSeconds = $state(300);
	let defaultIntervalSeconds = $state(300);
	let intervalSaving = $state(false);

	// 멀티 슬롯: ON이면 모니터링/주기/타입제외 변경을 모든 슬롯에 일괄 적용
	let applyToAllSlots = $state(false);
	const targetSlots = $derived<SlotTarget[]>(
		applyToAllSlots && allSlots.length > 1 ? allSlots : [activeSlot]
	);

	// Flattened data
	let flatEntries = $state<Record<string, any>[]>([]);
	let numberKeys = $state<string[]>([]);
	let stringKeys = $state<string[]>([]);
	let objectKeys = $state<string[]>([]);
	let arrayKeys = $state<string[]>([]);
	let selectedChartKeys = $state<Set<string>>(new Set());
	let selectedArrayKeys = $state<Set<string>>(new Set());
	let deltaKeys = $state<Set<string>>(new Set());
	let yAxisName = $state('');
	let chartType = $state<'line' | 'scatter'>('line');

	// View mode — iostat/heatmap/bitmap은 typeKey 접두사 매칭으로 동적 표시
	type ViewTab = 'chart' | 'table' | 'tree' | 'iostat' | 'heatmap' | 'bitmap';
	let viewTab = $state<ViewTab>('chart');
	let fullscreen = $state(false);

	// Key 검색 / 선택만 보기 / 그룹 접기
	let keyFilterQuery = $state('');
	let showSelectedOnly = $state(false);
	let collapsedGroups = $state<Set<string>>(new Set());
	let keyPanelCollapsed = $state(false);

	// Polling
	let pollTimer: ReturnType<typeof setInterval> | undefined;

	// 슬롯 탭 변경 시 데이터 리로드
	$effect(() => {
		if (open && activeSlot) {
			loadTypes();
			startPolling();
		} else {
			stopPolling();
			reset();
		}
		return () => stopPolling();
	});

	function reset() {
		metadataTypes = [];
		selectedTypeKey = null;
		entries = [];
		flatEntries = [];
		numberKeys = [];
		stringKeys = [];
		objectKeys = [];
		selectedChartKeys = new Set();
		deltaKeys = new Set();
		yAxisName = '';
		chartType = 'line';
		slotStatus = null;
		availableTypeKeys = new Set();
		savedFilePaths = new Map();
		keyFilterQuery = '';
		showSelectedOnly = false;
		collapsedGroups = new Set();
		keyPanelCollapsed = false;
	}

	async function loadTypes() {
		loading = true;
		const s = activeSlot;
		try {
			// testTrId가 있으면 TR 기반 조회 (더 정확), 없으면 slot의 controller/nandType/cellType으로 fallback
			if (s.testTrId && s.testTrId > 0 && s.headType !== undefined) {
				metadataTypes = await fetchMetadataForTr(s.testTrId, s.headType);
				// TR 매핑에서 결과 없으면 product 기반으로도 시도
				if (metadataTypes.length === 0 && (s.controller || s.nandType || s.cellType)) {
					metadataTypes = await fetchMetadataForProduct(s.controller, s.nandType, s.cellType);
				}
			} else {
				metadataTypes = await fetchMetadataForProduct(s.controller, s.nandType, s.cellType);
			}
			slotStatus = await fetchSlotMetadataStatus(s.tentacleName, s.slotNumber);
			try {
				const excl = await fetchExcludedTypes(s.tentacleName, s.slotNumber);
				excludedTypeKeys = new Set(excl.excludedTypes ?? []);
			} catch { excludedTypeKeys = new Set(); }
			try {
				const en = await fetchSlotMetadataEnabled(s.tentacleName, s.slotNumber);
				monitorEnabled = en.enabled;
			} catch { monitorEnabled = false; }
			try {
				const iv = await fetchSlotInterval(s.tentacleName, s.slotNumber);
				intervalSeconds = iv.intervalSeconds;
				defaultIntervalSeconds = iv.defaultIntervalSeconds;
			} catch { /* ignore */ }
			// 실제로 존재하는 debug_*.json 목록을 가져와 typeKey 매핑
			try {
				const filesRaw = await fetchSlotMetadataFiles(s.tentacleName, s.slotNumber, s.logPath, s.tentacleIp);
				const keys = new Set<string>();
				const pathMap = new Map<string, string>();
				for (const line of (filesRaw ?? '').split(/\r?\n/)) {
					const trimmed = line.trim();
					if (!trimmed) continue;
					const fname = trimmed.split('/').pop() ?? '';
					const m = fname.match(/^debug_(.+)\.json$/);
					if (m) {
						const key = m[1];
						keys.add(key);
						if (!pathMap.has(key)) pathMap.set(key, trimmed);
					}
				}
				availableTypeKeys = keys;
				savedFilePaths = pathMap;
			} catch { /* ignore */ }
		} catch (e: any) {
			toast.error('Metadata 타입 로드 실패: ' + e.message);
		} finally {
			loading = false;
		}
	}

	async function toggleCollect() {
		monitorToggling = true;
		const nextEnabled = !monitorEnabled;
		const targets = targetSlots;
		try {
			const results = await Promise.allSettled(
				targets.map((s) => setSlotMetadataEnabled(s.tentacleName, s.slotNumber, nextEnabled))
			);
			const failed = results.filter((r) => r.status === 'rejected').length;
			const ok = results.length - failed;
			// activeSlot 기준으로 UI 상태 갱신
			const activeRes = results[targets.indexOf(activeSlot)];
			if (activeRes && activeRes.status === 'fulfilled') {
				monitorEnabled = (activeRes.value as { enabled: boolean }).enabled;
			} else {
				monitorEnabled = nextEnabled;
			}
			if (failed === 0) {
				toast.success(
					targets.length > 1
						? `${ok}개 슬롯 모니터링 ${nextEnabled ? '시작' : '중지'}`
						: (nextEnabled ? '모니터링 시작' : '모니터링 중지')
				);
			} else {
				toast.error(`${ok}개 성공 · ${failed}개 실패`);
			}
			slotStatus = await fetchSlotMetadataStatus(activeSlot.tentacleName, activeSlot.slotNumber);
		} catch (e: any) {
			toast.error(e.message);
		} finally {
			monitorToggling = false;
		}
	}

	async function saveInterval() {
		if (intervalSeconds < 10) { toast.error('최소 10초'); return; }
		intervalSaving = true;
		const targets = targetSlots;
		try {
			const results = await Promise.allSettled(
				targets.map((s) => setSlotInterval(s.tentacleName, s.slotNumber, intervalSeconds))
			);
			const failed = results.filter((r) => r.status === 'rejected').length;
			const ok = results.length - failed;
			const activeRes = results[targets.indexOf(activeSlot)];
			if (activeRes && activeRes.status === 'fulfilled') {
				intervalSeconds = (activeRes.value as { intervalSeconds: number }).intervalSeconds;
			}
			if (failed === 0) {
				toast.success(
					targets.length > 1
						? `${ok}개 슬롯 주기: ${intervalSeconds}초`
						: `모니터링 주기: ${intervalSeconds}초`
				);
			} else {
				toast.error(`${ok}개 성공 · ${failed}개 실패`);
			}
		} catch (e: any) {
			toast.error(e.message);
		} finally {
			intervalSaving = false;
		}
	}

	async function toggleTypeCollection(typeKey: string) {
		const next = new Set(excludedTypeKeys);
		if (next.has(typeKey)) next.delete(typeKey); else next.add(typeKey);
		excludedTypeKeys = next;
		const targets = targetSlots;
		try {
			const results = await Promise.allSettled(
				targets.map((s) => setExcludedTypes(s.tentacleName, s.slotNumber, [...next]))
			);
			const failed = results.filter((r) => r.status === 'rejected').length;
			if (failed > 0) {
				toast.error(`${failed}개 슬롯 설정 실패`);
			} else if (targets.length > 1) {
				toast.success(`${targets.length}개 슬롯에 적용됨`);
			}
		} catch (e: any) {
			toast.error('설정 실패: ' + e.message);
		}
	}

	async function selectType(typeKey: string) {
		selectedTypeKey = typeKey;
		loadingData = true;
		deltaKeys = new Set();
		yAxisName = '';
		// typeKey 접두사 기반으로 전용 뷰 자동 전환
		if (typeKey.startsWith('iostat_info')) viewTab = 'iostat';
		else if (typeKey.startsWith('segment_info')) viewTab = 'heatmap';
		else if (typeKey.startsWith('victim_bits') || typeKey.startsWith('segment_bits')) viewTab = 'bitmap';
		else viewTab = 'chart';
		try {
			// 1) 모니터링 중이고 in-memory에 있으면 실시간 데이터 우선
			if (slotStatus?.monitoring && slotStatus.types?.includes(typeKey)) {
				entries = await fetchSlotMetadata(activeSlot.tentacleName, activeSlot.slotNumber, typeKey);
			} else if (savedFilePaths.has(typeKey)) {
				// 2) 파일 스캔에서 발견된 실제 경로 사용
				const raw = await fetchMetadataFile(activeSlot.tentacleName, savedFilePaths.get(typeKey)!, activeSlot.tentacleIp);
				entries = typeof raw === 'string' ? JSON.parse(raw) : raw;
			} else if (activeSlot.logPath) {
				// 3) logPath 디렉토리 추정 (성능: 파일명 포함, 호환성: 디렉토리)
				//    상대 경로면 /home/octo/tentacle/history/ 하위로 해석
				const hasExt = /\.[^/]+$/.test(activeSlot.logPath);
				let dirPath = hasExt ? activeSlot.logPath.replace(/\/[^/]+$/, '') : activeSlot.logPath;
				if (!dirPath.startsWith('/')) dirPath = `/home/octo/tentacle/history/${dirPath}`;
				const dir = dirPath.endsWith('/') ? dirPath : dirPath + '/';
				const raw = await fetchMetadataFile(activeSlot.tentacleName, `${dir}debug_${typeKey}.json`, activeSlot.tentacleIp);
				entries = typeof raw === 'string' ? JSON.parse(raw) : raw;
			} else {
				// 4) 기본 슬롯 경로
				const path = `/home/octo/tentacle/slot${activeSlot.slotNumber}/log/debug_${typeKey}.json`;
				const raw = await fetchMetadataFile(activeSlot.tentacleName, path, activeSlot.tentacleIp);
				entries = typeof raw === 'string' ? JSON.parse(raw) : raw;
			}
			processEntries();
		} catch (e: any) {
			toast.error('Metadata 로드 실패: ' + e.message);
			entries = [];
		} finally {
			loadingData = false;
		}
	}

	// localStorage key: 선택 기억용. typeKey별로 분리.
	function prefsStorageKey(typeKey: string): string {
		return `metadataDialog.chartKeys.v1:${typeKey}`;
	}

	interface StoredPrefs {
		chart?: string[];
		array?: string[];
		delta?: string[];
	}

	function loadStoredPrefs(typeKey: string): StoredPrefs {
		if (typeof localStorage === 'undefined') return {};
		try {
			const raw = localStorage.getItem(prefsStorageKey(typeKey));
			if (!raw) return {};
			const parsed = JSON.parse(raw);
			return (parsed && typeof parsed === 'object') ? parsed : {};
		} catch {
			return {};
		}
	}

	function persistPrefs() {
		if (typeof localStorage === 'undefined' || !selectedTypeKey) return;
		try {
			const prefs: StoredPrefs = {
				chart: [...selectedChartKeys],
				array: [...selectedArrayKeys],
				delta: [...deltaKeys]
			};
			localStorage.setItem(prefsStorageKey(selectedTypeKey), JSON.stringify(prefs));
		} catch { /* ignore quota etc */ }
	}

	function processEntries() {
		if (entries.length === 0) {
			flatEntries = [];
			numberKeys = [];
			stringKeys = [];
			objectKeys = [];
			arrayKeys = [];
			return;
		}
		flatEntries = entries.map((entry) => {
			const { time, ...rest } = entry;
			const flattened = flattenObject(rest);
			return { time, ...flattened };
		});
		const classified = classifyKeys(flatEntries);
		numberKeys = classified.numberKeys;
		stringKeys = classified.stringKeys;
		objectKeys = classified.objectKeys;
		arrayKeys = classified.arrayKeys;

		// 저장된 선택 복원 — 없거나 교집합 없으면 전체 해제 상태로 시작
		const stored = selectedTypeKey ? loadStoredPrefs(selectedTypeKey) : {};
		const numberSet = new Set(numberKeys);
		const arraySet = new Set(arrayKeys);
		selectedChartKeys = new Set((stored.chart ?? []).filter((k) => numberSet.has(k)));
		selectedArrayKeys = new Set((stored.array ?? []).filter((k) => arraySet.has(k)));
		deltaKeys = new Set((stored.delta ?? []).filter((k) => numberSet.has(k) || arraySet.has(k)));
	}

	function toggleChartKey(key: string) {
		const next = new Set(selectedChartKeys);
		if (next.has(key)) next.delete(key);
		else next.add(key);
		selectedChartKeys = next;
		persistPrefs();
	}

	function toggleDeltaKey(key: string) {
		const next = new Set(deltaKeys);
		if (next.has(key)) next.delete(key);
		else next.add(key);
		deltaKeys = next;
		persistPrefs();
	}

	// key 이름을 "." 기준으로 첫 세그먼트(=그룹)와 나머지(=하위 라벨)로 분리
	function splitKey(key: string): { group: string; sub: string } {
		const idx = key.indexOf('.');
		if (idx < 0) return { group: '(기타)', sub: key };
		return { group: key.slice(0, idx), sub: key.slice(idx + 1) };
	}

	interface KeyGroup {
		group: string;
		keys: string[]; // 필터/선택만보기 적용 후 화면에 실제 표시할 키
		totalInGroup: number; // 필터 적용 전 그룹 내 총 키 수
		selectedInGroup: number;
	}

	function buildGroups(
		allKeys: string[],
		selected: Set<string>,
		query: string,
		selectedOnly: boolean
	): KeyGroup[] {
		const q = query.trim().toLowerCase();
		const byGroup = new Map<string, string[]>();
		const totals = new Map<string, number>();
		const selectedCount = new Map<string, number>();
		for (const k of allKeys) {
			const { group } = splitKey(k);
			totals.set(group, (totals.get(group) ?? 0) + 1);
			if (selected.has(k)) selectedCount.set(group, (selectedCount.get(group) ?? 0) + 1);
			const pass =
				(!q || k.toLowerCase().includes(q)) &&
				(!selectedOnly || selected.has(k));
			if (!pass) continue;
			if (!byGroup.has(group)) byGroup.set(group, []);
			byGroup.get(group)!.push(k);
		}
		const out: KeyGroup[] = [];
		for (const [group, keys] of byGroup) {
			out.push({
				group,
				keys,
				totalInGroup: totals.get(group) ?? 0,
				selectedInGroup: selectedCount.get(group) ?? 0
			});
		}
		out.sort((a, b) => a.group.localeCompare(b.group));
		return out;
	}

	const numberGroups = $derived(
		buildGroups(numberKeys, selectedChartKeys, keyFilterQuery, showSelectedOnly)
	);
	const arrayGroups = $derived(
		buildGroups(arrayKeys, selectedArrayKeys, keyFilterQuery, showSelectedOnly)
	);

	// 그룹이 1개이고 이름이 "(기타)"면 헤더 숨기기 판단용
	const showGroupHeaders = $derived(numberGroups.length > 1 || (numberGroups[0]?.group ?? '') !== '(기타)');

	function toggleGroupCollapsed(group: string) {
		const next = new Set(collapsedGroups);
		if (next.has(group)) next.delete(group);
		else next.add(group);
		collapsedGroups = next;
	}

	function selectGroup(group: string, keys: string[], kind: 'chart' | 'array') {
		const target = kind === 'chart' ? selectedChartKeys : selectedArrayKeys;
		const next = new Set(target);
		const allIn = keys.every((k) => next.has(k));
		if (allIn) {
			for (const k of keys) next.delete(k);
		} else {
			for (const k of keys) next.add(k);
		}
		if (kind === 'chart') selectedChartKeys = next;
		else selectedArrayKeys = next;
		persistPrefs();
	}

	function toggleArrayKey(key: string) {
		const next = new Set(selectedArrayKeys);
		if (next.has(key)) next.delete(key);
		else next.add(key);
		selectedArrayKeys = next;
		persistPrefs();
	}

	function clearAllSelections() {
		selectedChartKeys = new Set();
		selectedArrayKeys = new Set();
		deltaKeys = new Set();
		persistPrefs();
	}

	// 타입 등록은 없지만 파일이 있는 경우, 파일 기반으로 가상 타입 생성
	const effectiveTypes = $derived.by(() => {
		const registered = new Map(metadataTypes.map((t) => [t.typeKey, t]));
		const merged: MetadataType[] = [...metadataTypes];
		for (const key of availableTypeKeys) {
			if (!registered.has(key)) {
				merged.push({
					id: -1,
					name: key,
					typeKey: key,
					category: 'unregistered',
					enabled: true,
					description: null
				});
			}
		}
		return merged;
	});

	// Delta가 적용된 데이터
	const displayEntries = $derived.by(() => {
		if (flatEntries.length === 0 || deltaKeys.size === 0) return flatEntries;
		return applyDelta(flatEntries, deltaKeys);
	});

	// Chart option
	const chartOption = $derived.by(() => {
		if (displayEntries.length === 0 || selectedChartKeys.size === 0) return null;

		const keys = [...selectedChartKeys];
		const selectedType = effectiveTypes.find((t) => t.typeKey === selectedTypeKey);
		const title = selectedType ? selectedType.name : selectedTypeKey ?? '';

		return {
			title: {
				text: title,
				subtext: fwVer || '',
				left: 'center',
				textStyle: { fontSize: 14 }
			},
			tooltip: { trigger: 'axis' as const },
			legend: { show: keys.length > 1, bottom: 0 },
			grid: { left: 60, right: 20, top: 60, bottom: keys.length > 1 ? 60 : 30 },
			xAxis: {
				type: 'value' as const,
				name: 'Time (sec)',
				nameLocation: 'middle' as const,
				nameGap: 25
			},
			yAxis: {
				type: 'value' as const,
				name: yAxisName || undefined
			},
			dataZoom: [{ type: 'inside' as const }],
			series: keys.map((key) => ({
				name: deltaKeys.has(key) ? `${key} (Δ)` : key,
				type: chartType as 'line' | 'scatter',
				data: displayEntries.map((e) => [e.time, e[key]]),
				showSymbol: chartType === 'scatter' || displayEntries.length < 50,
				symbolSize: chartType === 'scatter' ? 4 : undefined
			}))
		};
	});

	// Heatmap options (배열 키용)
	const heatmapOptions = $derived.by(() => {
		if (displayEntries.length === 0 || selectedArrayKeys.size === 0) return [];
		return [...selectedArrayKeys].map((key) => {
			const arr = displayEntries[0]?.[key];
			if (!Array.isArray(arr)) return null;
			const times = displayEntries.map((e) => String(e.time));
			const indices = Array.from({ length: arr.length }, (_, i) => String(i));
			const data: [number, number, number][] = [];
			let min = Infinity, max = -Infinity;
			for (let ti = 0; ti < displayEntries.length; ti++) {
				const vals = displayEntries[ti][key];
				if (!Array.isArray(vals)) continue;
				for (let ai = 0; ai < vals.length; ai++) {
					const v = typeof vals[ai] === 'number' ? vals[ai] : 0;
					data.push([ti, ai, v]);
					if (v < min) min = v;
					if (v > max) max = v;
				}
			}
			return {
				title: { text: deltaKeys.has(key) ? `${key} (Δ)` : key, left: 'center', textStyle: { fontSize: 13 } },
				tooltip: {
					formatter: (p: any) => `time: ${times[p.value[0]]}, index: ${p.value[1]}, value: ${p.value[2]}`
				},
				grid: { left: 60, right: 80, top: 40, bottom: 60 },
				xAxis: { type: 'category' as const, data: times, name: 'Time (sec)', splitArea: { show: true } },
				yAxis: { type: 'category' as const, data: indices, name: 'Index', splitArea: { show: true } },
				visualMap: {
					min: min === Infinity ? 0 : min,
					max: max === -Infinity ? 1 : max,
					calculable: true,
					orient: 'vertical' as const,
					right: 0,
					top: 'center',
					inRange: { color: ['#313695', '#4575b4', '#74add1', '#abd9e9', '#fee090', '#fdae61', '#f46d43', '#d73027', '#a50026'] }
				},
				series: [{ type: 'heatmap' as const, data, label: { show: false }, emphasis: { itemStyle: { shadowBlur: 10, shadowColor: 'rgba(0,0,0,0.5)' } } }]
			};
		}).filter(Boolean);
	});

	// DataTable columns
	const tableColumns = $derived.by((): ColumnDef<Record<string, any>, unknown>[] => {
		if (displayEntries.length === 0) return [];
		const allKeys = Object.keys(displayEntries[0]).filter((k) => k !== 'time');
		return [
			{ accessorKey: 'time', header: 'Time (sec)', size: 80 },
			...allKeys.map((key) => ({
				accessorKey: key,
				header: deltaKeys.has(key) ? `${key} (Δ)` : key,
				cell: ({ row }: any) => {
					const val = row.original[key];
					return val !== undefined && val !== null ? String(val) : '';
				}
			}))
		];
	});

	// Polling for live data
	function startPolling() {
		stopPolling();
		pollTimer = setInterval(async () => {
			if (!open || !selectedTypeKey) return;
			try {
				slotStatus = await fetchSlotMetadataStatus(activeSlot.tentacleName, activeSlot.slotNumber);
				if (slotStatus?.monitoring && selectedTypeKey) {
					const newEntries = await fetchSlotMetadata(activeSlot.tentacleName, activeSlot.slotNumber, selectedTypeKey);
					if (newEntries.length > entries.length) {
						entries = newEntries;
						processEntries();
					}
				}
			} catch {
				// silent
			}
		}, 30000);
	}

	async function exportExcel() {
		const { exportToExcel } = await import('$lib/utils/excel-export');

		const selectedType = effectiveTypes.find((t) => t.typeKey === selectedTypeKey);
		const sheetName = selectedType?.name ?? selectedTypeKey ?? 'Metadata';
		const headers = ['Time (sec)', ...numberKeys.map((k) => deltaKeys.has(k) ? `${k} (Δ)` : k)];
		const rows = displayEntries.map((e) =>
			[e.time, ...numberKeys.map((k) => typeof e[k] === 'number' ? e[k] : '')] as (string | number)[]
		);

		await exportToExcel({
			fileName: `${sheetName}_${activeSlot.tentacleName}_Slot${activeSlot.slotNumber}.xlsx`,
			sheets: [{ name: sheetName.slice(0, 31), sections: [{ type: 'table', title: sheetName, headers, rows }] }]
		});
	}

	function exportJson() {
		const selectedType = effectiveTypes.find((t) => t.typeKey === selectedTypeKey);
		const name = selectedType?.name ?? selectedTypeKey ?? 'Metadata';
		const jsonStr = JSON.stringify(entries, null, 2);
		const blob = new Blob([jsonStr], { type: 'application/json' });
		const url = URL.createObjectURL(blob);
		const a = document.createElement('a');
		a.href = url;
		a.download = `${name}_${activeSlot.tentacleName}_Slot${activeSlot.slotNumber}.json`;
		a.click();
		URL.revokeObjectURL(url);
	}

	function stopPolling() {
		if (pollTimer) {
			clearInterval(pollTimer);
			pollTimer = undefined;
		}
	}
</script>

<Dialog.Root bind:open onOpenChange={(v) => { if (!v) onClose(); }}>
	<Dialog.Content class={fullscreen
		? 'w-screen h-screen max-w-none max-h-none sm:max-w-none rounded-none overflow-hidden flex flex-col p-4'
		: 'w-[90vw] sm:max-w-[1400px] h-[92vh] max-h-[92vh] overflow-hidden flex flex-col'}>
		<!-- shadcn Dialog.Content의 X 버튼(right-4 top-4) 바로 왼쪽에 확대/축소 배치 -->
		<button
			class="absolute right-10 top-4 inline-flex items-center rounded-sm p-1 text-muted-foreground opacity-70 hover:opacity-100 hover:bg-muted transition-opacity z-50"
			onclick={() => (fullscreen = !fullscreen)}
			title={fullscreen ? '일반 크기' : '전체화면'}
			aria-label={fullscreen ? '일반 크기' : '전체화면'}
		>
			{#if fullscreen}
				<MinimizeIcon class="size-4" />
			{:else}
				<MaximizeIcon class="size-4" />
			{/if}
		</button>
		<Dialog.Header>
			<Dialog.Title class="flex items-center gap-2 pr-16">
				<DatabaseIcon class="size-5" />
				<span class="flex-1 truncate">UFS Metadata{allSlots.length > 1 ? ` (${allSlots.length}개 슬롯)` : ` — ${activeSlot.tentacleName} Slot${activeSlot.slotNumber}`}</span>
			</Dialog.Title>
			<Dialog.Description>
				{#if slotStatus?.monitoring}
					<span class="text-green-600">Monitoring</span> — {Math.floor((slotStatus.elapsedSeconds ?? 0) / 60)}min {(slotStatus.elapsedSeconds ?? 0) % 60}s elapsed
				{:else}
					Saved metadata viewer
				{/if}
			</Dialog.Description>
		</Dialog.Header>

		<!-- 모니터링 컨트롤 바 — readOnly(History) 모드에서는 숨김 -->
		{#if !readOnly && !loading && metadataTypes.length > 0}
			<div class="flex items-center gap-4 px-1 py-2 border-b">
				<div class="flex items-center gap-2">
					<span class="text-xs text-muted-foreground">모니터링:</span>
					<button
						class="px-2.5 py-1 text-xs rounded-md font-medium transition-colors {monitorEnabled ? 'bg-green-500 text-white' : 'bg-muted text-muted-foreground'}"
						onclick={toggleCollect}
						disabled={monitorToggling}
					>
						{monitorToggling ? '...' : monitorEnabled ? 'ON' : 'OFF'}
					</button>
				</div>
				<div class="flex items-center gap-1.5">
					<span class="text-xs text-muted-foreground">주기:</span>
					<input
						type="number"
						class="border rounded px-2 py-0.5 text-xs w-16 bg-background"
						min={10}
						bind:value={intervalSeconds}
					/>
					<span class="text-xs text-muted-foreground">초</span>
					<button
						class="px-2 py-0.5 text-[10px] rounded border hover:bg-muted text-muted-foreground"
						onclick={saveInterval}
						disabled={intervalSaving}
					>
						{intervalSaving ? '...' : '적용'}
					</button>
					{#if intervalSeconds !== defaultIntervalSeconds}
						<span class="text-[10px] text-blue-600">(기본: {defaultIntervalSeconds}초)</span>
					{/if}
				</div>
				{#if slotStatus?.monitoring}
					<span class="text-[10px] text-green-600 flex items-center gap-1">
						<span class="inline-block size-1.5 rounded-full bg-green-500 animate-pulse"></span>
						{Math.floor((slotStatus.elapsedSeconds ?? 0) / 60)}m {(slotStatus.elapsedSeconds ?? 0) % 60}s
					</span>
				{/if}
				{#if allSlots.length > 1}
					<label class="ml-auto flex items-center gap-1.5 text-xs cursor-pointer select-none" title="모니터링 ON/OFF, 주기, 타입 제외 토글을 {allSlots.length}개 슬롯 모두에 적용">
						<input type="checkbox" bind:checked={applyToAllSlots} class="size-3.5 accent-primary" />
						<span class={applyToAllSlots ? 'font-medium text-primary' : 'text-muted-foreground'}>
							모든 슬롯 일괄 적용 ({allSlots.length})
						</span>
					</label>
				{/if}
			</div>
		{/if}

		<div class="flex-1 overflow-auto space-y-4 px-1">
			{#if loading}
				<div class="flex items-center justify-center py-12">
					<LoaderIcon class="size-6 animate-spin text-muted-foreground" />
				</div>
			{:else if metadataTypes.length === 0 && availableTypeKeys.size === 0}
				<div class="text-center py-12 text-muted-foreground space-y-1">
					<div>이 제품에 등록된 metadata 타입이 없습니다.</div>
					<div class="text-[10px] text-muted-foreground/70">
						tentacle={activeSlot.tentacleName} · slot={activeSlot.slotNumber} · trId={activeSlot.testTrId ?? 'n/a'} · headType={activeSlot.headType ?? 'n/a'}
					</div>
				</div>
			{:else}
				<!-- 슬롯 탭 (멀티 슬롯일 때만) -->
				{#if allSlots.length > 1}
					<div class="flex gap-1 border-b pb-2">
						{#each allSlots as s, i}
							<button
								class="px-3 py-1.5 text-xs rounded-t-md transition-colors {activeSlotIdx === i
									? 'bg-primary text-primary-foreground'
									: 'bg-muted text-muted-foreground hover:bg-muted/80'}"
								onclick={() => { activeSlotIdx = i; }}
							>
								{s.tentacleName}-{s.slotNumber}
							</button>
						{/each}
					</div>
				{/if}

				<!-- Metadata type selector with collection toggle -->
				<div class="flex flex-wrap gap-2">
					{#each effectiveTypes as type}
						{@const hasFile = availableTypeKeys.has(type.typeKey)}
						{@const liveAvail = slotStatus?.monitoring && slotStatus.types?.includes(type.typeKey)}
						<div class="inline-flex items-center rounded-md overflow-hidden border {selectedTypeKey === type.typeKey ? 'border-primary' : 'border-transparent'}">
							<button
								class="px-3 py-1.5 text-xs font-medium transition-colors {selectedTypeKey === type.typeKey
									? 'bg-primary text-primary-foreground'
									: excludedTypeKeys.has(type.typeKey) ? 'bg-muted/50 text-muted-foreground/50 line-through' : !hasFile && !liveAvail ? 'bg-muted/30 text-muted-foreground/40' : 'bg-muted text-muted-foreground hover:bg-muted/80'}"
								onclick={() => selectType(type.typeKey)}
								title={hasFile ? `저장된 파일: ${savedFilePaths.get(type.typeKey) ?? ''}` : !liveAvail ? '저장된 데이터 없음' : undefined}
							>
								{type.name}
								{#if liveAvail}
									<span class="ml-1 inline-block size-1.5 rounded-full bg-green-500" title="모니터링 중"></span>
								{:else if hasFile}
									<span class="ml-1 inline-block size-1.5 rounded-full bg-blue-400" title="저장된 파일 있음"></span>
								{/if}
							</button>
							<button
								class="px-1.5 py-1.5 text-[10px] transition-colors {excludedTypeKeys.has(type.typeKey) ? 'bg-red-50 text-red-400 hover:text-red-600' : 'bg-green-50 text-green-600 hover:text-green-800'}"
								onclick={() => toggleTypeCollection(type.typeKey)}
								title={excludedTypeKeys.has(type.typeKey) ? '모니터링 OFF — 클릭하여 ON' : '모니터링 ON — 클릭하여 OFF'}
							>
								{excludedTypeKeys.has(type.typeKey) ? 'OFF' : 'ON'}
							</button>
						</div>
					{/each}
				</div>

				{#if loadingData}
					<div class="flex items-center justify-center py-8">
						<LoaderIcon class="size-5 animate-spin text-muted-foreground" />
					</div>
				{:else if selectedTypeKey && entries.length > 0}
					{@const isIostat = selectedTypeKey.startsWith('iostat_info')}
					{@const isSegment = selectedTypeKey.startsWith('segment_info')}
					{@const isBitmap = selectedTypeKey.startsWith('victim_bits') || selectedTypeKey.startsWith('segment_bits')}
					<!-- View tabs -->
					<Tabs bind:value={viewTab}>
						<TabsList>
							{#if isIostat}
								<TabsTrigger value="iostat">Iostat</TabsTrigger>
							{/if}
							{#if isSegment}
								<TabsTrigger value="heatmap">Heatmap</TabsTrigger>
							{/if}
							{#if isBitmap}
								<TabsTrigger value="bitmap">Bitmap</TabsTrigger>
							{/if}
							{#if numberKeys.length > 0 || arrayKeys.length > 0}
								<TabsTrigger value="chart">Chart</TabsTrigger>
							{/if}
							<TabsTrigger value="table">Table</TabsTrigger>
							<TabsTrigger value="tree">Tree View</TabsTrigger>
						</TabsList>

						{#if isIostat}
							<TabsContent value="iostat">
								<IostatTableView entries={entries as any[]} fileLabel="{activeSlot.tentacleName}_slot{activeSlot.slotNumber}_{selectedTypeKey}" />
							</TabsContent>
						{/if}
						{#if isSegment}
							<TabsContent value="heatmap">
								<SegmentHeatmap entries={entries as any[]} />
							</TabsContent>
						{/if}
						{#if isBitmap}
							<TabsContent value="bitmap">
								{@const bitKey = selectedTypeKey.startsWith('segment_bits') ? 'segment_bits' : 'victim_bits'}
								<BitmapGridView entries={entries as any[]} format={bitKey} />
							</TabsContent>
						{/if}

						{#if numberKeys.length > 0 || arrayKeys.length > 0}
							<TabsContent value="chart" class="space-y-3">
								<!-- Key selector 헤더 (접기/펼치기) -->
								<button
									class="w-full flex items-center gap-2 px-2 py-1.5 rounded-md border bg-muted/30 hover:bg-muted/50 transition-colors"
									onclick={() => (keyPanelCollapsed = !keyPanelCollapsed)}
									aria-expanded={!keyPanelCollapsed}
								>
									<ChevronRightIcon class="size-3 transition-transform {keyPanelCollapsed ? '' : 'rotate-90'}" />
									<span class="text-[11px] font-medium">키 선택</span>
									<span class="text-[10px] text-muted-foreground">
										{selectedChartKeys.size + selectedArrayKeys.size} / {numberKeys.length + arrayKeys.length} 선택
									</span>
								</button>

								{#if !keyPanelCollapsed}
								<!-- Key selector: 검색 + 선택 요약 + 그룹화 -->
								<div class="space-y-2">
									<!-- 검색/요약 바 -->
									<div class="flex items-center gap-2 flex-wrap">
										<div class="flex items-center gap-1.5 flex-1 min-w-[220px] h-7 rounded-md border border-border bg-background px-2 focus-within:ring-2 focus-within:ring-primary/40">
											<SearchIcon class="size-3.5 text-muted-foreground shrink-0" />
											<input
												type="text"
												class="flex-1 min-w-0 h-full text-[11px] bg-transparent placeholder:text-muted-foreground focus:outline-none"
												placeholder="키 검색 (예: sda21, utilization)"
												bind:value={keyFilterQuery}
											/>
											{#if keyFilterQuery}
												<button
													class="inline-flex items-center justify-center size-4 rounded text-muted-foreground hover:text-foreground hover:bg-muted shrink-0"
													onclick={() => (keyFilterQuery = '')}
													aria-label="검색어 지우기"
												>
													<XIcon class="size-3" />
												</button>
											{/if}
										</div>
										<button
											class="h-7 px-2.5 text-[10px] rounded-md border transition-colors {showSelectedOnly ? 'bg-primary text-primary-foreground border-primary' : 'border-border hover:bg-muted text-muted-foreground'}"
											onclick={() => (showSelectedOnly = !showSelectedOnly)}
											title="선택된 키만 보기"
										>
											선택만 ({selectedChartKeys.size + selectedArrayKeys.size})
										</button>
										{#if selectedChartKeys.size + selectedArrayKeys.size > 0}
											<button
												class="h-7 px-2.5 text-[10px] rounded-md text-muted-foreground hover:text-destructive hover:bg-muted transition-colors"
												onclick={clearAllSelections}
											>
												모두 해제
											</button>
										{/if}
									</div>

									<!-- 선택된 키 칩 (빠르게 식별) -->
									{#if selectedChartKeys.size > 0}
										<div class="flex flex-wrap gap-1">
											{#each [...selectedChartKeys] as key (key)}
												<span class="inline-flex items-center gap-1 rounded-full bg-blue-50 px-2 py-0.5 text-[10px] text-blue-700">
													<span class="font-mono">{key}</span>
													<button
														class="hover:text-blue-900"
														onclick={() => toggleChartKey(key)}
														aria-label="해제"
													>×</button>
												</span>
											{/each}
										</div>
									{/if}

									<!-- 그룹 단위 키 리스트 -->
									<div class="space-y-1.5">
										{#each numberGroups as g (g.group)}
											{@const collapsed = collapsedGroups.has(g.group)}
											<div class="border rounded-md overflow-hidden">
												{#if showGroupHeaders}
													<div class="flex items-center gap-2 px-2 py-1 bg-muted/40 text-[10px]">
														<button
															class="inline-flex items-center gap-1 text-muted-foreground hover:text-foreground"
															onclick={() => toggleGroupCollapsed(g.group)}
														>
															<ChevronRightIcon class="size-3 transition-transform {collapsed ? '' : 'rotate-90'}" />
															<span class="font-medium">{g.group}</span>
														</button>
														<span class="text-muted-foreground/70">
															{g.selectedInGroup}/{g.totalInGroup}
														</span>
														<button
															class="ml-auto text-[10px] text-primary hover:underline"
															onclick={() => selectGroup(g.group, g.keys, 'chart')}
														>
															{g.keys.every((k) => selectedChartKeys.has(k)) ? '그룹 해제' : '그룹 선택'}
														</button>
													</div>
												{/if}
												{#if !collapsed}
													<div class="flex flex-wrap gap-1 p-1.5">
														{#each g.keys as key (key)}
															{@const selected = selectedChartKeys.has(key)}
															{@const label = showGroupHeaders ? splitKey(key).sub : key}
															<div class="inline-flex items-center rounded border {selected ? 'border-blue-300' : 'border-transparent'}">
																<button
																	class="rounded-l px-2 py-0.5 text-[10px] font-mono transition-colors {selected ? 'bg-blue-100 text-blue-700' : 'bg-muted text-muted-foreground hover:bg-muted/80'}"
																	onclick={() => toggleChartKey(key)}
																	title={key}
																>
																	{label}
																</button>
																{#if selected}
																	<button
																		class="rounded-r px-1 py-0.5 text-[9px] font-bold transition-colors {deltaKeys.has(key) ? 'bg-orange-100 text-orange-700' : 'bg-gray-100 text-gray-400 hover:text-orange-500'}"
																		onclick={() => toggleDeltaKey(key)}
																		title="Delta: 이전 값과의 차이 (시작=0)"
																	>
																		Δ
																	</button>
																{/if}
															</div>
														{/each}
													</div>
												{/if}
											</div>
										{:else}
											<div class="text-[11px] text-muted-foreground text-center py-3">
												{keyFilterQuery ? '일치하는 키 없음' : showSelectedOnly ? '선택된 키 없음' : '사용 가능한 키 없음'}
											</div>
										{/each}
									</div>
								</div>

								<!-- 배열 키 (히트맵용) -->
								{#if arrayGroups.length > 0}
									<div class="space-y-1.5">
										<div class="text-[10px] font-medium text-muted-foreground">배열 (히트맵)</div>
										{#each arrayGroups as g (g.group)}
											{@const collapsed = collapsedGroups.has(`arr:${g.group}`)}
											<div class="border rounded-md overflow-hidden">
												{#if showGroupHeaders}
													<div class="flex items-center gap-2 px-2 py-1 bg-muted/40 text-[10px]">
														<button
															class="inline-flex items-center gap-1 text-muted-foreground hover:text-foreground"
															onclick={() => toggleGroupCollapsed(`arr:${g.group}`)}
														>
															<ChevronRightIcon class="size-3 transition-transform {collapsed ? '' : 'rotate-90'}" />
															<span class="font-medium">{g.group}</span>
														</button>
														<span class="text-muted-foreground/70">
															{g.selectedInGroup}/{g.totalInGroup}
														</span>
														<button
															class="ml-auto text-[10px] text-primary hover:underline"
															onclick={() => selectGroup(g.group, g.keys, 'array')}
														>
															{g.keys.every((k) => selectedArrayKeys.has(k)) ? '그룹 해제' : '그룹 선택'}
														</button>
													</div>
												{/if}
												{#if !collapsed}
													<div class="flex flex-wrap gap-1 p-1.5">
														{#each g.keys as key (key)}
															{@const arr = flatEntries[0]?.[key]}
															{@const len = Array.isArray(arr) ? arr.length : 0}
															{@const selected = selectedArrayKeys.has(key)}
															{@const label = showGroupHeaders ? splitKey(key).sub : key}
															<div class="inline-flex items-center rounded border {selected ? 'border-purple-300' : 'border-transparent'}">
																<button
																	class="rounded-l px-2 py-0.5 text-[10px] font-mono transition-colors {selected ? 'bg-purple-100 text-purple-700' : 'bg-muted text-muted-foreground hover:bg-muted/80'}"
																	onclick={() => toggleArrayKey(key)}
																	title={key}
																>
																	{label} ({len})
																</button>
																{#if selected}
																	<button
																		class="rounded-r px-1 py-0.5 text-[9px] font-bold transition-colors {deltaKeys.has(key) ? 'bg-orange-100 text-orange-700' : 'bg-gray-100 text-gray-400 hover:text-orange-500'}"
																		onclick={() => toggleDeltaKey(key)}
																		title="Delta: 이전 값과의 차이 (시작=0)"
																	>
																		Δ
																	</button>
																{/if}
															</div>
														{/each}
													</div>
												{/if}
											</div>
										{/each}
									</div>
								{/if}
								{/if}

								<!-- Y축 이름 + Line/Scatter -->
								<div class="flex items-center gap-4">
									<div class="flex items-center gap-2">
										<span class="{captionMuted}">Y축:</span>
										<input
											class="border rounded px-2 py-0.5 text-[10px] w-40"
											placeholder="Y축 이름 (예: EC Count)"
											bind:value={yAxisName}
										/>
									</div>
									<div class="flex rounded-md border overflow-hidden">
										<button
											class="px-2.5 py-1 text-[11px] transition-colors {chartType === 'line' ? 'bg-primary text-primary-foreground' : 'hover:bg-muted text-muted-foreground'}"
											onclick={() => (chartType = 'line')}
										>Line</button>
										<button
											class="px-2.5 py-1 text-[11px] transition-colors {chartType === 'scatter' ? 'bg-primary text-primary-foreground' : 'hover:bg-muted text-muted-foreground'}"
											onclick={() => (chartType = 'scatter')}
										>Scatter</button>
									</div>
									<button
										class="px-2.5 py-1 text-[11px] rounded-md border flex items-center gap-1 hover:bg-muted text-muted-foreground transition-colors"
										onclick={exportExcel}
										title="Export to Excel"
									>
										<Download class="size-3" />
										Excel
									</button>
									<button
										class="px-2.5 py-1 text-[11px] rounded-md border flex items-center gap-1 hover:bg-muted text-muted-foreground transition-colors"
										onclick={exportJson}
										title="Export to JSON"
									>
										<Download class="size-3" />
										JSON
									</button>
								</div>

								{#if chartOption}
									<div class="resize-y overflow-hidden rounded border bg-card" style="height: {fullscreen ? '480px' : '320px'}; min-height: 200px;">
										<PerfChart option={chartOption} height="100%" />
									</div>
								{/if}
								{#each heatmapOptions as heatOpt}
									<div class="resize-y overflow-hidden rounded border bg-card" style="height: {fullscreen ? '420px' : '300px'}; min-height: 200px;">
										<PerfChart option={heatOpt} height="100%" />
									</div>
								{/each}
							</TabsContent>
						{/if}

						<TabsContent value="table">
							<DataTable
								data={displayEntries}
								columns={tableColumns}
								scrollHeight={fullscreen ? '70vh' : '55vh'}
								enableColumnVisibility={true}
							/>
						</TabsContent>

						<TabsContent value="tree">
							<div class={fullscreen ? 'max-h-[75vh] overflow-auto' : 'max-h-[60vh] overflow-auto'}>
								<JsonView result={entries} />
							</div>
						</TabsContent>
					</Tabs>
				{:else if selectedTypeKey}
					<div class="text-center py-8 text-muted-foreground text-sm">
						데이터가 없습니다.
					</div>
				{/if}
			{/if}
		</div>
	</Dialog.Content>
</Dialog.Root>
