import { sendHeadCommand } from '$lib/api/testdb.js';
import type { HeadSlotData } from '$lib/api/headSlotStore.svelte.js';
import type { SlotInfomation, CompatibilityTestCase } from '$lib/api/types.js';

export type CommandVariant = 'settc' | 'settc2';

export interface QuickTimePreset {
	key: string;
	label: string;
	d: number;
	h: number;
	m: number;
}

export const QUICK_TIME_PRESETS: QuickTimePreset[] = [
	{ key: '40min', label: '40min', d: 0, h: 0, m: 40 },
	{ key: '4h', label: '4h', d: 0, h: 4, m: 0 },
	{ key: '7d', label: '7d', d: 7, h: 0, m: 0 }
];

/** 분 단위 → "Xd Yh Zm" 짧은 표기. 0 이면 "-". */
export function formatCompatTime(totalMin: number): string {
	if (!totalMin || totalMin <= 0) return '-';
	const d = Math.floor(totalMin / (24 * 60));
	const h = Math.floor((totalMin - d * 24 * 60) / 60);
	const m = totalMin - d * 24 * 60 - h * 60;
	const parts: string[] = [];
	if (d > 0) parts.push(`${d}d`);
	if (h > 0) parts.push(`${h}h`);
	if (m > 0) parts.push(`${m}m`);
	return parts.join(' ');
}

/** "T1-0" → "0" */
export function extractSlotPosition(setLocation: string): string {
	const match = setLocation.match(/-(\d+)$/);
	return match ? match[1] : '0';
}

/** TC 옵션 Map 의 형식 — tcId → {key: value} */
export type PickedTcMap = Map<number, Record<string, string>>;

interface SlotItemLite {
	slot: SlotInfomation;
	headData?: HeadSlotData;
}

interface BuildOptionsCommon {
	commandVariant: CommandVariant;
	pickedTcs: PickedTcMap;
}

interface BuildOptionsCompat extends BuildOptionsCommon {
	isCompatTab: true;
	compatTCs: CompatibilityTestCase[];
	compatTestTimeMin: number;
}

interface BuildOptionsPerf extends BuildOptionsCommon {
	isCompatTab: false;
	headData: HeadSlotData | undefined;
	slot: SlotInfomation;
}

export type BuildOptions = BuildOptionsCompat | BuildOptionsPerf;

/**
 * 한 슬롯에 보낼 settc/settc2 명령 payload 를 빌드.
 * 호환성: settc2 는 일괄 1회, settc 는 TC 별 반복 → 호출자가 chunk 단위로 처리하도록 chunks 반환.
 * 성능: 항상 1회.
 */
export function buildSettcChunks(opts: BuildOptions): { command: CommandVariant; data: string }[] {
	const chunks: { command: CommandVariant; data: string }[] = [];
	const tcEntries: [number, Record<string, string>][] = [];
	opts.pickedTcs.forEach((o, tcId) => tcEntries.push([tcId, o]));

	if (opts.isCompatTab) {
		const tcOptionMap = new Map(opts.compatTCs.map((tc) => [tc.id, tc.tcOption ?? '']));
		const tcTestTypeMap = new Map(opts.compatTCs.map((tc) => [tc.id, tc.testType ?? '']));

		if (opts.commandVariant === 'settc2') {
			const BT = '`';
			const data = tcEntries
				.map(([tcId]) => {
					const tcOption = tcOptionMap.get(tcId) ?? '';
					const testType = tcTestTypeMap.get(tcId) ?? '';
					return `${tcId}${BT}${testType}${BT}${opts.compatTestTimeMin}${BT}${tcOption}^`;
				})
				.join('')
				.replace(/\r?\n/g, '');
			chunks.push({ command: 'settc2', data });
		} else {
			// settc: HEAD 가 복수 TC 동시 수신 미지원 → TC 1 개씩 반복
			for (const [tcId] of tcEntries) {
				const tcOption = tcOptionMap.get(tcId) ?? '';
				const testType = tcTestTypeMap.get(tcId) ?? '';
				const data =
					`tcid;${tcId},testtype;${testType},testtime;${opts.compatTestTimeMin},${tcOption}`.replace(
						/\r?\n/g,
						''
					);
				chunks.push({ command: 'settc', data });
			}
		}
	} else {
		const trName = opts.headData?.testTrName || '';
		const lcMatch = trName.match(/\b([A-Z]*LC)\b/i);
		const c = lcMatch ? lcMatch[1].toUpperCase() : '';
		const u = opts.headData?.usbId || '';
		const p = extractSlotPosition(opts.headData?.setLocation || opts.slot.tentacleName || '');

		const data = tcEntries
			.map(([tcId, optsMap]) => {
				const optParts = Object.entries(optsMap)
					.map(([k, v]) => `${k}$${v}`)
					.join(';');
				if (opts.commandVariant === 'settc2') {
					const BT = '`';
					return `${tcId}${BT}${optParts};c$${c};u$${u};p$${p}^`;
				}
				return `tcid;${tcId}#options;${optParts};c$${c};u$${u};p$${p}^`;
			})
			.join('')
			.replace(/\r?\n/g, '');

		chunks.push({ command: opts.commandVariant, data });
	}

	return chunks;
}

export interface ApplySettcArgs {
	source: string; // activeTab
	isCompatTab: boolean;
	commandVariant: CommandVariant;
	pickedTcs: PickedTcMap;
	compatTCs: CompatibilityTestCase[];
	compatTestTimeMin: number;
	selectedItems: SlotItemLite[];
}

export interface ApplySettcResult {
	successCount: number;
	totalCount: number;
	lastError: string;
}

/**
 * 선택된 슬롯들에 settc 명령을 발송. 호환성/성능, settc/settc2 분기를 모두 처리.
 * 호출자는 결과를 토스트로 표시하면 됨.
 */
export async function applySettcToSlots(args: ApplySettcArgs): Promise<ApplySettcResult> {
	let successCount = 0;
	let lastError = '';
	const totalCount = args.selectedItems.length;

	for (const item of args.selectedItems) {
		const slotIdx = item.headData?.slotIndex ?? item.slot.slotNumber ?? 0;
		const chunks = args.isCompatTab
			? buildSettcChunks({
					isCompatTab: true,
					commandVariant: args.commandVariant,
					pickedTcs: args.pickedTcs,
					compatTCs: args.compatTCs,
					compatTestTimeMin: args.compatTestTimeMin
				})
			: buildSettcChunks({
					isCompatTab: false,
					commandVariant: args.commandVariant,
					pickedTcs: args.pickedTcs,
					headData: item.headData,
					slot: item.slot
				});

		let perSlotOk = true;
		for (const c of chunks) {
			try {
				await sendHeadCommand({
					source: args.source,
					command: c.command,
					slotNumbers: [slotIdx],
					data: c.data
				});
			} catch (e: any) {
				lastError = e?.message ?? String(e);
				perSlotOk = false;
			}
		}
		if (perSlotOk) successCount++;
	}

	return { successCount, totalCount, lastError };
}
