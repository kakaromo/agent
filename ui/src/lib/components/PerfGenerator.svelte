<script lang="ts">
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Badge } from '$lib/components/ui/badge';
	import * as Card from '$lib/components/ui/card';
	import CodeIcon from '@lucide/svelte/icons/code';
	import CopyIcon from '@lucide/svelte/icons/copy';
	import CheckIcon from '@lucide/svelte/icons/check';
	import AlertCircleIcon from '@lucide/svelte/icons/circle-alert';
	import EyeIcon from '@lucide/svelte/icons/eye';
	import JsonTreeView from './JsonTreeView.svelte';
	import PerfPreview from './PerfPreview.svelte';

	import { untrack } from 'svelte';
	import type { FieldRole, FieldType, TopLevelShape, FieldNode, TabInfo, AnalysisResult } from './PerfGenerator.types';

	// --- State ---
	let jsonInput = $state('');
	let componentName = $state('MyPerf');
	let xAxisUnit = $state('GB');
	let includeExcelExport = $state(false);
	let copied = $state(false);
	let rightPanelTab = $state<'code' | 'preview'>('code');
	let jsonPanelTab = $state<'text' | 'tree'>('text');

	// Parsed data for preview
	const parsedData = $derived.by(() => {
		if (!jsonInput.trim()) return undefined;
		try {
			return JSON.parse(jsonInput);
		} catch {
			return undefined;
		}
	});

	// User overrides: keyed by field path string
	let fieldRoleOverrides = $state<Record<string, FieldRole>>({});
	let tabOverrides = $state<Record<string, { label: string; yAxisUnit: string }>>({});

	// --- Utility ---
	function capitalize(s: string): string {
		return s.charAt(0).toUpperCase() + s.slice(1);
	}

	function guessYAxisUnit(key: string): string {
		const k = key.toLowerCase();
		if (/rand/.test(k)) return 'IOPS';
		if (/seq/.test(k)) return 'MB/s';
		if (/time|lat/.test(k)) return 'ms';
		return 'Value';
	}

	function pathStr(path: string[]): string {
		return path.join('.');
	}

	// --- Recursive Field Flattener ---
	function flattenFields(obj: Record<string, unknown>, path: string[] = []): FieldNode[] {
		const nodes: FieldNode[] = [];
		for (const [key, value] of Object.entries(obj)) {
			const currentPath = [...path, key];
			if (value === null || value === undefined) {
				nodes.push({ path: currentPath, key, type: 'null', sample: null, role: 'ignore' });
			} else if (Array.isArray(value) && value.length > 0 && value.every((v) => typeof v === 'number')) {
				nodes.push({ path: currentPath, key, type: 'number[]', sample: value, role: 'data' });
			} else if (typeof value === 'number') {
				const role: FieldRole = /cycle|id|index/i.test(key) ? 'cycle' : 'stat';
				nodes.push({ path: currentPath, key, type: 'number', sample: value, role });
			} else if (typeof value === 'string') {
				nodes.push({ path: currentPath, key, type: 'string', sample: value, role: 'ignore' });
			} else if (typeof value === 'object' && !Array.isArray(value)) {
				// Recurse into nested objects
				nodes.push(...flattenFields(value as Record<string, unknown>, currentPath));
			} else {
				// Arrays of non-numbers, etc.
				nodes.push({ path: currentPath, key, type: 'object', sample: value, role: 'ignore' });
			}
		}
		return nodes;
	}

	// --- Top-level JSON Analysis ---
	function analyzeJson(input: string): AnalysisResult {
		if (!input.trim()) return { shape: 'other', tabs: [], cycleField: null, allFields: [], error: undefined };

		let parsed: unknown;
		try {
			parsed = JSON.parse(input);
		} catch (e) {
			return { shape: 'other', tabs: [], cycleField: null, allFields: [], error: `JSON parse error: ${(e as Error).message}` };
		}

		// Case 1: Top-level object → keys with array values become tabs
		if (typeof parsed === 'object' && parsed !== null && !Array.isArray(parsed)) {
			const obj = parsed as Record<string, unknown>;
			const arrayKeys = Object.keys(obj).filter((k) => Array.isArray(obj[k]) && (obj[k] as unknown[]).length > 0);

			if (arrayKeys.length === 0) {
				return { shape: 'other', tabs: [], cycleField: null, allFields: [], error: 'No array-valued keys found in object' };
			}

			const tabs: TabInfo[] = [];
			const allFields: FieldNode[] = [];
			let cycleField: FieldNode | null = null;

			for (const tabKey of arrayKeys) {
				const arr = obj[tabKey] as unknown[];
				const sample = arr[0];
				if (typeof sample !== 'object' || sample === null) continue;

				const fields = flattenFields(sample as Record<string, unknown>);
				// Apply user overrides
				for (const f of fields) {
					const ps = pathStr(f.path);
					if (fieldRoleOverrides[ps] !== undefined) {
						f.role = fieldRoleOverrides[ps];
					}
				}

				if (!cycleField) {
					cycleField = fields.find((f) => f.role === 'cycle') ?? null;
				}

				tabs.push({
					key: tabKey.toLowerCase(),
					label: capitalize(tabKey),
					yAxisUnit: guessYAxisUnit(tabKey),
					fields
				});
				allFields.push(...fields);
			}

			return { shape: 'object-of-arrays', tabs, cycleField, allFields };
		}

		// Case 2: Top-level array → each item's object-valued keys become tabs
		if (Array.isArray(parsed)) {
			if (parsed.length === 0) return { shape: 'other', tabs: [], cycleField: null, allFields: [], error: 'Empty array' };

			const sample = parsed[0];
			if (typeof sample !== 'object' || sample === null) {
				return { shape: 'other', tabs: [], cycleField: null, allFields: [], error: 'Array items are not objects' };
			}

			const sampleObj = sample as Record<string, unknown>;

			// Top-level fields of array items (cycle candidates, etc.)
			const topFields: FieldNode[] = [];
			const tabKeys: string[] = [];

			for (const [key, value] of Object.entries(sampleObj)) {
				if (typeof value === 'object' && value !== null && !Array.isArray(value)) {
					tabKeys.push(key);
				} else if (typeof value === 'number') {
					const role: FieldRole = /cycle|id|index/i.test(key) ? 'cycle' : 'stat';
					topFields.push({ path: [key], key, type: 'number', sample: value, role });
				} else if (typeof value === 'string') {
					topFields.push({ path: [key], key, type: 'string', sample: value, role: 'ignore' });
				} else if (value === null) {
					topFields.push({ path: [key], key, type: 'null', sample: null, role: 'ignore' });
				}
			}

			// Apply overrides to top fields
			for (const f of topFields) {
				const ps = pathStr(f.path);
				if (fieldRoleOverrides[ps] !== undefined) {
					f.role = fieldRoleOverrides[ps];
				}
			}

			let cycleField = topFields.find((f) => f.role === 'cycle') ?? null;

			if (tabKeys.length === 0) {
				// No nested objects — treat the entire item as a single implicit tab
				const fields = flattenFields(sampleObj);
				for (const f of fields) {
					const ps = pathStr(f.path);
					if (fieldRoleOverrides[ps] !== undefined) {
						f.role = fieldRoleOverrides[ps];
					}
				}
				if (!cycleField) cycleField = fields.find((f) => f.role === 'cycle') ?? null;
				return {
					shape: 'array-of-objects',
					tabs: [{ key: 'default', label: 'Data', yAxisUnit: 'Value', fields }],
					cycleField,
					allFields: fields
				};
			}

			const tabs: TabInfo[] = [];
			const allFields: FieldNode[] = [...topFields];

			for (const tabKey of tabKeys) {
				const nested = sampleObj[tabKey] as Record<string, unknown>;
				const fields = flattenFields(nested);
				// Apply overrides
				for (const f of fields) {
					// Prefix tab key to path for uniqueness
					f.path = [tabKey, ...f.path];
					const ps = pathStr(f.path);
					if (fieldRoleOverrides[ps] !== undefined) {
						f.role = fieldRoleOverrides[ps];
					}
				}

				tabs.push({
					key: tabKey.toLowerCase(),
					label: capitalize(tabKey),
					yAxisUnit: guessYAxisUnit(tabKey),
					fields
				});
				allFields.push(...fields);
			}

			return { shape: 'array-of-objects', tabs, cycleField, allFields };
		}

		return { shape: 'other', tabs: [], cycleField: null, allFields: [], error: 'Expected a top-level object or array' };
	}

	const analysis: AnalysisResult = $derived.by(() => analyzeJson(jsonInput));

	// Init tab overrides when tabs change
	$effect(() => {
		const tabs = analysis.tabs;
		untrack(() => {
			const newOverrides: Record<string, { label: string; yAxisUnit: string }> = {};
			for (const tab of tabs) {
				newOverrides[tab.key] = tabOverrides[tab.key] ?? {
					label: tab.label,
					yAxisUnit: tab.yAxisUnit
				};
			}
			tabOverrides = newOverrides;
		});
	});

	const mergedTabs = $derived(
		analysis.tabs.map((t) => ({
			...t,
			label: tabOverrides[t.key]?.label ?? t.label,
			yAxisUnit: tabOverrides[t.key]?.yAxisUnit ?? t.yAxisUnit
		}))
	);

	// Collect unique fields for the field mapping table (deduplicated by path)
	const uniqueFields = $derived.by(() => {
		const seen = new Set<string>();
		const result: FieldNode[] = [];
		for (const f of analysis.allFields) {
			const ps = pathStr(f.path);
			if (!seen.has(ps)) {
				seen.add(ps);
				result.push(f);
			}
		}
		// Also include cycle field if it's a top-level field not in tabs
		if (analysis.cycleField) {
			const ps = pathStr(analysis.cycleField.path);
			if (!seen.has(ps)) {
				seen.add(ps);
				result.push(analysis.cycleField);
			}
		}
		return result;
	});

	function setFieldRole(field: FieldNode, role: FieldRole) {
		const ps = pathStr(field.path);
		fieldRoleOverrides[ps] = role;
		// Force re-analysis by triggering reactivity
		fieldRoleOverrides = { ...fieldRoleOverrides };
	}

	function sampleDisplay(sample: unknown): string {
		if (sample === null || sample === undefined) return 'null';
		if (Array.isArray(sample)) {
			const preview = sample.slice(0, 3).join(', ');
			return `[${preview}${sample.length > 3 ? ', ...' : ''}]`;
		}
		if (typeof sample === 'number') return String(sample);
		if (typeof sample === 'string') return `"${sample.slice(0, 20)}"`;
		return String(sample);
	}

	// --- Code Generation ---
	function generateCode(): string {
		if (analysis.error || mergedTabs.length === 0) return '';

		const cycleField = analysis.cycleField;
		const cyclePath = cycleField ? cycleField.path : [];

		// Determine if any tab has data (number[]) fields
		const hasDataFields = mergedTabs.some((t) => t.fields.some((f) => f.role === 'data'));

		if (analysis.shape === 'object-of-arrays') {
			return generateObjectOfArrays(hasDataFields, cyclePath);
		} else {
			return generateArrayOfObjects(hasDataFields, cyclePath);
		}
	}

	function generateObjectOfArrays(hasDataFields: boolean, cyclePath: string[]): string {
		const sample = mergedTabs[0];
		const cycleAccessor = cyclePath.length > 0 ? cyclePath[cyclePath.length - 1] : 'cycle';

		// Build interface fields from first tab
		const interfaceFields = sample.fields
			.map((f) => {
				const name = f.path[f.path.length - 1];
				if (f.role === 'data') return `\t\t${name}: number[];`;
				if (f.role === 'stat') return `\t\t${name}: number;`;
				if (f.role === 'cycle') return `\t\t${name}: number;`;
				if (f.role === 'ignore' && f.type === 'null') return `\t\t${name}: unknown;`;
				return `\t\t${name}: unknown;`;
			})
			.join('\n');

		const tabDefs = mergedTabs
			.map((t) => `\t\t{ key: '${t.key}', label: '${t.label}' }`)
			.join(',\n');

		const statFields = sample.fields.filter((f) => f.role === 'stat');
		const dataFields = sample.fields.filter((f) => f.role === 'data');
		const dataField = dataFields.length > 0 ? dataFields[0].path[dataFields[0].path.length - 1] : '';
		const hasTabs = mergedTabs.length > 1;
		const yAxisMaxType = hasTabs ? 'record' : (hasDataFields ? 'number' : false);

		const statsColDefs = statFields
			.map((f) => {
				const name = f.path[f.path.length - 1];
				return `\t\t{\n\t\t\taccessorKey: '${name}',\n\t\t\theader: '${name}',\n\t\t\tcell: ({ row }) => row.original.${name}.toFixed(2)\n\t\t}`;
			})
			.join(',\n');

		const chartImport = hasDataFields ? `\n\timport { PerfChart } from '$lib/components/perf-chart';` : '';
		const echartsImport = hasDataFields ? `\n\timport type { EChartsOption } from 'echarts';` : '';
		const perfChartTypeImport = hasDataFields && includeExcelExport
			? `\n\timport type { PerfChart as PerfChartType } from '$lib/components/perf-chart';`
			: '';
		const baseChartOptImport = hasDataFields
			? `\n\timport { baseChartOption } from './perfChartUtils';`
			: '';
		const perfStylesImport = `\n\timport { btnBase, btnActive, btnInactive${hasTabs ? ', btnDisabled' : ''}, groupClass } from './perfStyles';`;
		const emptyStateImport = hasDataFields
			? `\n\timport { emptyState } from '$lib/styles/common.js';`
			: '';
		const downloadIconImport = hasDataFields && includeExcelExport
			? `\n\timport Download from '@lucide/svelte/icons/download';`
			: '';

		const chartStateVars = hasDataFields
			? `\n\t${includeExcelExport ? 'let chartRef: ReturnType<typeof PerfChartType> | undefined = $state();\n\t' : ''}let chartType = $state<'line' | 'scatter'>('line');\n\tlet showRawData = $state(true);`
			: `\n\tlet showRawData = $state(true);`;

		const hasValidDataFn = hasDataFields
			? `\n\n\tfunction hasValidData(cycles: CycleEntry[]): boolean {\n\t\treturn cycles.length > 0 && cycles.some((c) => c.${dataField}?.length > 0);\n\t}`
			: `\n\n\tfunction hasValidData(cycles: CycleEntry[]): boolean {\n\t\treturn cycles.length > 0;\n\t}`;

		const indicesDerived = hasDataFields
			? `\n\n\tconst indices = $derived(() => {\n\t\tconst maxLen = currentCycles.reduce((m, c) => Math.max(m, c.${dataField}?.length ?? 0), 0);\n\t\treturn Array.from({ length: maxLen }, (_, i) => i);\n\t});`
			: '';

		// yAxisMax in chart
		const yAxisMaxLine = yAxisMaxType === 'record'
			? `\n\t\t\t...(yAxisMax?.[activeTab] != null ? { max: yAxisMax[activeTab] } : {})`
			: yAxisMaxType === 'number'
				? `\n\t\t\t...(yAxisMax != null ? { max: yAxisMax } : {})`
				: '';

		const chartOption = hasDataFields
			? `\n\n\tconst chartOption: EChartsOption = $derived({
\t\t...baseChartOption(chartTitle, fw, { left: 90 }),
\t\txAxis: {
\t\t\ttype: 'category',
\t\t\tdata: indices().map(String),
\t\t\tname: '${xAxisUnit}',
\t\t\tnameLocation: 'center',
\t\t\tnameGap: 25
\t\t},
\t\tyAxis: {
\t\t\ttype: 'value',
\t\t\tnameLocation: 'center',
\t\t\tnameRotate: 90,
\t\t\tnameGap: 50,${yAxisMaxLine}
\t\t},
\t\tseries: currentCycles.map((entry) => ({
\t\t\tname: \\\`Cycle \\\${entry.${cycleAccessor}}\\\`,
\t\t\ttype: chartType,
\t\t\tdata: entry.${dataField},
\t\t\tsymbolSize: chartType === 'scatter' ? 4 : undefined,
\t\t\tsmooth: false
\t\t}))
\t});`
			: '';

		const dataRowTypes = hasDataFields
			? `\n\n\ttype DataRow = Record<string, number>;\n\n\tconst dataRows: DataRow[] = $derived(\n\t\tindices().map((idx) => {\n\t\t\tconst row: DataRow = { index: idx };\n\t\t\tfor (const entry of currentCycles) {\n\t\t\t\trow[\\\`c\\\${entry.${cycleAccessor}}\\\`] = entry.${dataField}[idx] ?? 0;\n\t\t\t}\n\t\t\treturn row;\n\t\t})\n\t);\n\n\tconst dataColumns: ColumnDef<DataRow, unknown>[] = $derived([\n\t\t{ accessorKey: 'index', header: 'Index', enableSorting: true },\n\t\t...currentCycles.map((entry) => ({\n\t\t\taccessorKey: \\\`c\\\${entry.${cycleAccessor}}\\\`,\n\t\t\theader: \\\`Cycle \\\${entry.${cycleAccessor}}\\\`,\n\t\t\tenableSorting: true\n\t\t}))\n\t]);`
			: '';

		// yAxisMax prop type
		const yAxisMaxProp = yAxisMaxType === 'record'
			? `\n\t\tyAxisMax?: Record<string, number>;`
			: yAxisMaxType === 'number'
				? `\n\t\tyAxisMax?: number;`
				: '';
		const yAxisMaxDestructure = yAxisMaxType ? ', yAxisMax' : '';

		// Excel export function (opt-in via settings)
		const exportExcelFn = hasDataFields && includeExcelExport
			? `\n\n\tasync function exportExcel() {
\t\tconst { exportToExcel, renderChartToImage } = await import('$lib/utils/excel-export');
\t\tconst chartImage = chartRef?.getImageDataURL() ?? await renderChartToImage(chartOption);
\t\tconst statsHeaders = ['Cycle', ${statFields.map((f) => `'${f.path[f.path.length - 1]}'`).join(', ')}];
\t\tconst statsRows = currentCycles.map((c) => [\\\`Cycle \\\${c.${cycleAccessor}}\\\`, ${statFields.map((f) => `c.${f.path[f.path.length - 1]}`).join(', ')}] as (string | number)[]);
\t\tconst rawHeaders = ['Index', ...currentCycles.map((c) => \\\`Cycle \\\${c.${cycleAccessor}}\\\`)];
\t\tconst rawRows = indices().map((idx) => [idx, ...currentCycles.map((c) => c.${dataField}[idx] ?? '')] as (string | number)[]);
\t\tawait exportToExcel({
\t\t\tfileName: \\\`\\\${tcName}_\\\${activeLabel}.xlsx\\\`,
\t\t\tsheets: [{
\t\t\t\tname: activeLabel,
\t\t\t\tsections: [
\t\t\t\t\t{ type: 'image', imageDataURL: chartImage, headers: [], rows: [] },
\t\t\t\t\t{ type: 'table', title: 'Statistics', headers: statsHeaders, rows: statsRows },
\t\t\t\t\t{ type: 'table', title: 'Raw Data', headers: rawHeaders, rows: rawRows }
\t\t\t\t]
\t\t\t}]
\t\t});
\t}`
			: '';

		// Toolbar chart toggle UI
		const chartToggleUI = hasDataFields
			? `\n\n\t\t<div class={groupClass}>
\t\t\t<button
\t\t\t\tclass="{btnBase} {chartType === 'line' ? btnActive : btnInactive}"
\t\t\t\tonclick={() => (chartType = 'line')}
\t\t\t>Line</button>
\t\t\t<button
\t\t\t\tclass="{btnBase} {chartType === 'scatter' ? btnActive : btnInactive}"
\t\t\t\tonclick={() => (chartType = 'scatter')}
\t\t\t>Scatter</button>
\t\t</div>`
			: '';

		// Excel button (opt-in)
		const excelButton = hasDataFields && includeExcelExport
			? `\n\n\t\t{#if currentCycles.length > 0}
\t\t\t<div class="ml-auto">
\t\t\t\t<button
\t\t\t\t\tclass="{btnBase} {btnInactive} rounded-md border flex items-center gap-1"
\t\t\t\t\tonclick={exportExcel}
\t\t\t\t\ttitle="Export data as Excel"
\t\t\t\t>
\t\t\t\t\t<Download class="size-3" />
\t\t\t\t\tExcel
\t\t\t\t</button>
\t\t\t</div>
\t\t{/if}`
			: '';

		// Chart card — uses shared emptyState style
		const chartBindThis = includeExcelExport ? 'bind:this={chartRef} ' : '';
		const chartCard = hasDataFields
			? `\n\n\t<!-- Chart Card -->
\t<Card.Root class="gap-0 p-0 overflow-hidden">
\t\t<Card.Content class="p-2">
\t\t\t{#if currentCycles.length === 0}
\t\t\t\t<div class={emptyState}>
\t\t\t\t\t<span class="text-sm">No data for "{activeLabel || 'this tab'}"</span>
\t\t\t\t</div>
\t\t\t{:else}
\t\t\t\t<PerfChart ${chartBindThis}option={chartOption} height="420px" />
\t\t\t{/if}
\t\t</Card.Content>
\t</Card.Root>`
			: '';

		// Raw data table (stacked, not side-by-side)
		const rawDataTable = hasDataFields
			? `\n\n\t\t<!-- Raw Data -->
\t\t<Card.Root class="gap-0 p-0 overflow-hidden">
\t\t\t<SectionHeader title="Raw Data ({dataRows.length} points)">
\t\t\t\t<button
\t\t\t\t\tclass="text-[11px] text-muted-foreground hover:text-foreground transition-colors"
\t\t\t\t\tonclick={() => (showRawData = !showRawData)}
\t\t\t\t>
\t\t\t\t\t{showRawData ? 'Hide' : 'Show'}
\t\t\t\t</button>
\t\t\t</SectionHeader>
\t\t\t{#if showRawData}
\t\t\t\t<Card.Content class="p-2">
\t\t\t\t\t<DataTable
\t\t\t\t\t\tdata={dataRows}
\t\t\t\t\t\tcolumns={dataColumns}
\t\t\t\t\t\tcompact={true}
\t\t\t\t\t\tenableColumnVisibility={false}
\t\t\t\t\t\tscrollHeight="320px"
\t\t\t\t\t\tenableCellCopy={true}
\t\t\t\t\t\tgetRowId={(row) => String(row.index)}
\t\t\t\t\t/>
\t\t\t\t</Card.Content>
\t\t\t{/if}
\t\t</Card.Root>`
			: '';

		return `<script lang="ts">
\timport { type ColumnDef } from '@tanstack/table-core';${chartImport}
\timport { DataTable } from '$lib/components/data-table';
\timport * as Card from '$lib/components/ui/card';
\timport SectionHeader from './SectionHeader.svelte';${echartsImport}${perfChartTypeImport}${baseChartOptImport}${perfStylesImport}${emptyStateImport}${downloadIconImport}

\tinterface CycleEntry {
${interfaceFields}
\t}

\tconst TAB_DEFS = [
${tabDefs}
\t] as const;

\tinterface Props {
\t\tdata: Record<string, CycleEntry[]>;
\t\ttcName: string;
\t\tfw?: string;${yAxisMaxProp}
\t}

\tlet { data, tcName, fw${yAxisMaxDestructure} }: Props = $props();
${chartStateVars}
\tlet activeTab = $state('');

\tconst normalizedData = $derived(() => {
\t\tconst result: Record<string, CycleEntry[]> = {};
\t\tfor (const [key, value] of Object.entries(data)) {
\t\t\tif (Array.isArray(value)) {
\t\t\t\tresult[key.toLowerCase()] = value;
\t\t\t}
\t\t}
\t\treturn result;
\t});${hasValidDataFn}

\tconst availableTabs = $derived(
\t\tTAB_DEFS.filter((tab) => tab.key in normalizedData())
\t);

\t$effect(() => {
\t\tconst currentValid = availableTabs.some(
\t\t\t(t) => t.key === activeTab && hasValidData(normalizedData()[t.key] ?? [])
\t\t);
\t\tif (!currentValid) {
\t\t\tconst firstValid = availableTabs.find((t) => hasValidData(normalizedData()[t.key] ?? []));
\t\t\tif (firstValid) activeTab = firstValid.key;
\t\t}
\t});

\tconst currentCycles = $derived(normalizedData()[activeTab] ?? []);
\tconst activeLabel = $derived(availableTabs.find((t) => t.key === activeTab)?.label ?? '');
\tconst chartTitle = $derived(\\\`\\\${tcName} \\u2013 \\\${activeLabel}\\\`);${indicesDerived}${chartOption}${dataRowTypes}

\tconst statsColumns: ColumnDef<CycleEntry, unknown>[] = $derived([
\t\t{
\t\t\taccessorKey: '${cycleAccessor}',
\t\t\theader: 'Cycle',
\t\t\tcell: ({ row }) => \\\`Cycle \\\${row.original.${cycleAccessor}}\\\`
\t\t},
${statsColDefs}
\t]);${exportExcelFn}
<` + `/script>

<div class="space-y-3">
\t<!-- Toolbar -->
\t<div class="flex items-center gap-1.5 flex-wrap">
\t\t{#if availableTabs.length > 1}
\t\t\t<div class={groupClass}>
\t\t\t\t{#each availableTabs as tab (tab.key)}
\t\t\t\t\t{@const valid = hasValidData(normalizedData()[tab.key] ?? [])}
\t\t\t\t\t<button
\t\t\t\t\t\tclass="{btnBase} {activeTab === tab.key ? btnActive : !valid ? btnDisabled : btnInactive}"
\t\t\t\t\t\tonclick={() => valid && (activeTab = tab.key)}
\t\t\t\t\t\tdisabled={!valid}
\t\t\t\t\t>
\t\t\t\t\t\t{tab.label} <span class="opacity-60">({normalizedData()[tab.key]?.length ?? 0})</span>
\t\t\t\t\t</button>
\t\t\t\t{/each}
\t\t\t</div>
\t\t\t<div class="w-px h-5 bg-border"></div>
\t\t{/if}${chartToggleUI}${excelButton}
\t</div>${chartCard}

\t<!-- Tables -->
\t{#if currentCycles.length > 0}
\t\t<!-- Statistics -->
\t\t<Card.Root class="gap-0 p-0 overflow-hidden">
\t\t\t<SectionHeader title="Statistics" />
\t\t\t<Card.Content class="p-2">
\t\t\t\t<DataTable
\t\t\t\t\tdata={currentCycles}
\t\t\t\t\tcolumns={statsColumns}
\t\t\t\t\tshowPagination={false}
\t\t\t\t\tcompact={true}
\t\t\t\t\tenableColumnVisibility={false}
\t\t\t\t\tenableCellCopy={true}
\t\t\t\t\tgetRowId={(row) => String(row.${cycleAccessor})}
\t\t\t\t/>
\t\t\t</Card.Content>
\t\t</Card.Root>${rawDataTable}
\t{/if}
</div>`;
	}

	function generateArrayOfObjects(hasDataFields: boolean, cyclePath: string[]): string {
		const cycleAccessor = cyclePath.length > 0 ? cyclePath[cyclePath.length - 1] : 'cycle';

		const tabDefs = mergedTabs
			.map((t) => `\t\t{ key: '${t.key}', label: '${t.label}' }`)
			.join(',\n');

		// Collect stat fields across tabs (use first tab as representative)
		const sample = mergedTabs[0];
		const statFields = sample.fields.filter((f) => f.role === 'stat');
		const dataFields = sample.fields.filter((f) => f.role === 'data');
		const hasTabs = mergedTabs.length > 1;

		const statsColDefs = statFields
			.map((f) => {
				const safeKey = f.path.slice(1).join('_');
				const header = f.path.slice(1).join('.');
				return `\t\t{\n\t\t\taccessorKey: '${safeKey}',\n\t\t\theader: '${header}',\n\t\t\tcell: ({ row }) => {\n\t\t\t\tconst v = row.original['${safeKey}'];\n\t\t\t\treturn typeof v === 'number' ? v.toFixed(2) : String(v ?? '');\n\t\t\t}\n\t\t}`;
			})
			.join(',\n');

		// Build row extraction logic
		const rowExtraction = statFields
			.map((f) => {
				const safeKey = f.path.slice(1).join('_');
				const accessChain = f.path.slice(1).map((p) => `['${p}']`).join('?.');
				return `\t\t\trow['${safeKey}'] = typeof tabData?.${accessChain} === 'number' ? tabData${f.path.slice(1).map((p) => `['${p}']`).join('.')} as number : 0;`;
			})
			.join('\n');

		// Check for data fields in array-of-objects
		const hasData = dataFields.length > 0;
		const dataField = hasData ? dataFields[0] : null;
		const dataAccessChain = dataField ? dataField.path.slice(1).map((p) => `['${p}']`).join('?.') : '';

		const chartImport = hasData ? `\n\timport { PerfChart } from '$lib/components/perf-chart';` : '';
		const echartsImport = hasData ? `\n\timport type { EChartsOption } from 'echarts';` : '';
		const perfChartTypeImport = hasData && includeExcelExport
			? `\n\timport type { PerfChart as PerfChartType } from '$lib/components/perf-chart';`
			: '';
		const baseChartOptImport = hasData
			? `\n\timport { baseChartOption } from './perfChartUtils';`
			: '';
		const perfStylesImport = `\n\timport { btnBase, btnActive, btnInactive, groupClass } from './perfStyles';`;
		const emptyStateImport = hasData
			? `\n\timport { emptyState } from '$lib/styles/common.js';`
			: '';
		const downloadIconImport = hasData && includeExcelExport
			? `\n\timport Download from '@lucide/svelte/icons/download';`
			: '';
		const chartStateVars = hasData
			? `\n\t${includeExcelExport ? 'let chartRef: ReturnType<typeof PerfChartType> | undefined = $state();\n\t' : ''}let chartType = $state<'line' | 'scatter'>('line');\n\tlet showRawData = $state(true);`
			: `\n\tlet showRawData = $state(true);`;

		const dataSafeKey = dataField ? dataField.path.slice(1).join('_') : '';
		const dataExtraction = hasData
			? `\n\t\t\trow['${dataSafeKey}'] = Array.isArray(tabData?.${dataAccessChain}) ? tabData${dataField!.path.slice(1).map((p) => `['${p}']`).join('.')} as number[] : [];`
			: '';

		const chartAndDataSections = hasData
			? `\n\n\tconst indices = $derived(() => {
\t\tconst maxLen = statRows.reduce((m, r) => Math.max(m, ((r['${dataSafeKey}'] as number[]) ?? []).length), 0);
\t\treturn Array.from({ length: maxLen }, (_, i) => i);
\t});

\tconst chartOption: EChartsOption = $derived({
\t\t...baseChartOption(\\\`\\\${tcName} \\u2013 \\\${currentTab?.label ?? ''}\\\`, fw, { left: 90 }),
\t\txAxis: {
\t\t\ttype: 'category',
\t\t\tdata: indices().map(String),
\t\t\tname: '${xAxisUnit}',
\t\t\tnameLocation: 'center',
\t\t\tnameGap: 25
\t\t},
\t\tyAxis: {
\t\t\ttype: 'value',
\t\t\tnameLocation: 'center',
\t\t\tnameRotate: 90,
\t\t\tnameGap: 50
\t\t},
\t\tseries: statRows.map((row) => ({
\t\t\tname: String(row.cycle),
\t\t\ttype: chartType,
\t\t\tdata: (row['${dataSafeKey}'] as number[]) ?? [],
\t\t\tsymbolSize: chartType === 'scatter' ? 4 : undefined,
\t\t\tsmooth: false
\t\t}))
\t});

\ttype DataRow = Record<string, number>;

\tconst dataRows: DataRow[] = $derived(
\t\tindices().map((idx) => {
\t\t\tconst row: DataRow = { index: idx };
\t\t\tfor (const sr of statRows) {
\t\t\t\tconst arr = (sr['${dataSafeKey}'] as number[]) ?? [];
\t\t\t\trow[\\\`c\\\${sr.cycle}\\\`] = arr[idx] ?? 0;
\t\t\t}
\t\t\treturn row;
\t\t})
\t);

\tconst dataColumns: ColumnDef<DataRow, unknown>[] = $derived([
\t\t{ accessorKey: 'index', header: 'Index', enableSorting: true },
\t\t...statRows.map((sr) => ({
\t\t\taccessorKey: \\\`c\\\${sr.cycle}\\\`,
\t\t\theader: String(sr.cycle),
\t\t\tenableSorting: true
\t\t}))
\t]);`
			: '';

		// Excel export (opt-in)
		const exportExcelFn = hasData && includeExcelExport
			? `\n\n\tasync function exportExcel() {
\t\tconst { exportToExcel, renderChartToImage } = await import('$lib/utils/excel-export');
\t\tconst chartImage = chartRef?.getImageDataURL() ?? await renderChartToImage(chartOption);
\t\tconst statsHeaders = ['Cycle', ${statFields.map((f) => `'${f.path.slice(1).join('.')}'`).join(', ')}];
\t\tconst statsRs = statRows.map((r) => [r.cycle, ${statFields.map((f) => `r['${f.path.slice(1).join('_')}'] ?? ''`).join(', ')}] as (string | number)[]);
\t\tconst rawHeaders = ['Index', ...statRows.map((r) => String(r.cycle))];
\t\tconst rawRows = indices().map((idx) => [idx, ...statRows.map((r) => ((r['${dataSafeKey}'] as number[]) ?? [])[idx] ?? '')] as (string | number)[]);
\t\tawait exportToExcel({
\t\t\tfileName: \\\`\\\${tcName}_\\\${currentTab?.label ?? ''}.xlsx\\\`,
\t\t\tsheets: [{
\t\t\t\tname: currentTab?.label ?? 'Data',
\t\t\t\tsections: [
\t\t\t\t\t{ type: 'image', imageDataURL: chartImage, headers: [], rows: [] },
\t\t\t\t\t{ type: 'table', title: 'Statistics', headers: statsHeaders, rows: statsRs },
\t\t\t\t\t{ type: 'table', title: 'Raw Data', headers: rawHeaders, rows: rawRows }
\t\t\t\t]
\t\t\t}]
\t\t});
\t}`
			: '';

		const chartToggleUI = hasData
			? `\n\n\t\t<div class={groupClass}>
\t\t\t<button
\t\t\t\tclass="{btnBase} {chartType === 'line' ? btnActive : btnInactive}"
\t\t\t\tonclick={() => (chartType = 'line')}
\t\t\t>Line</button>
\t\t\t<button
\t\t\t\tclass="{btnBase} {chartType === 'scatter' ? btnActive : btnInactive}"
\t\t\t\tonclick={() => (chartType = 'scatter')}
\t\t\t>Scatter</button>
\t\t</div>`
			: '';

		const excelButton = hasData && includeExcelExport
			? `\n\n\t\t{#if statRows.length > 0}
\t\t\t<div class="ml-auto">
\t\t\t\t<button
\t\t\t\t\tclass="{btnBase} {btnInactive} rounded-md border flex items-center gap-1"
\t\t\t\t\tonclick={exportExcel}
\t\t\t\t\ttitle="Export data as Excel"
\t\t\t\t>
\t\t\t\t\t<Download class="size-3" />
\t\t\t\t\tExcel
\t\t\t\t</button>
\t\t\t</div>
\t\t{/if}`
			: '';

		const chartBindThis = includeExcelExport ? 'bind:this={chartRef} ' : '';
		const chartCard = hasData
			? `\n\n\t<!-- Chart Card -->
\t<Card.Root class="gap-0 p-0 overflow-hidden">
\t\t<Card.Content class="p-2">
\t\t\t{#if statRows.length === 0}
\t\t\t\t<div class={emptyState}>
\t\t\t\t\t<span class="text-sm">No data available</span>
\t\t\t\t</div>
\t\t\t{:else}
\t\t\t\t<PerfChart ${chartBindThis}option={chartOption} height="420px" />
\t\t\t{/if}
\t\t</Card.Content>
\t</Card.Root>`
			: '';

		const rawDataTable = hasData
			? `\n\n\t\t<!-- Raw Data -->
\t\t<Card.Root class="gap-0 p-0 overflow-hidden">
\t\t\t<SectionHeader title="Raw Data ({dataRows.length} points)">
\t\t\t\t<button
\t\t\t\t\tclass="text-[11px] text-muted-foreground hover:text-foreground transition-colors"
\t\t\t\t\tonclick={() => (showRawData = !showRawData)}
\t\t\t\t>
\t\t\t\t\t{showRawData ? 'Hide' : 'Show'}
\t\t\t\t</button>
\t\t\t</SectionHeader>
\t\t\t{#if showRawData}
\t\t\t\t<Card.Content class="p-2">
\t\t\t\t\t<DataTable
\t\t\t\t\t\tdata={dataRows}
\t\t\t\t\t\tcolumns={dataColumns}
\t\t\t\t\t\tcompact={true}
\t\t\t\t\t\tenableColumnVisibility={false}
\t\t\t\t\t\tscrollHeight="320px"
\t\t\t\t\t\tenableCellCopy={true}
\t\t\t\t\t\tgetRowId={(row) => String(row.index)}
\t\t\t\t\t/>
\t\t\t\t</Card.Content>
\t\t\t{/if}
\t\t</Card.Root>`
			: '';

		return `<script lang="ts">
\timport { type ColumnDef } from '@tanstack/table-core';${chartImport}
\timport { DataTable } from '$lib/components/data-table';
\timport * as Card from '$lib/components/ui/card';
\timport SectionHeader from './SectionHeader.svelte';${echartsImport}${perfChartTypeImport}${baseChartOptImport}${perfStylesImport}${emptyStateImport}${downloadIconImport}

\tinterface RawEntry {
\t\t${cycleAccessor}: number;
\t\t[key: string]: unknown;
\t}

\tconst TAB_DEFS = [
${tabDefs}
\t] as const;

\tinterface Props {
\t\tdata: RawEntry[];
\t\ttcName: string;
\t\tfw?: string;
\t}

\tlet { data, tcName, fw }: Props = $props();
${chartStateVars}
\tlet activeTab = $state(TAB_DEFS[0]?.key ?? '');

\tconst currentTab = $derived(TAB_DEFS.find((t) => t.key === activeTab));

\t// Extract stat rows for current tab from raw data
\ttype StatRow = Record<string, number | string | number[]>;

\tconst statRows: StatRow[] = $derived(
\t\tdata.map((entry) => {
\t\t\tconst tabData = entry[Object.keys(entry).find((k) => k.toLowerCase() === activeTab) ?? ''] as Record<string, unknown> | undefined;
\t\t\tconst row: StatRow = { cycle: \\\`Cycle \\\${entry.${cycleAccessor}}\\\` };
${rowExtraction}${dataExtraction}
\t\t\treturn row;
\t\t})
\t);

\tconst columns: ColumnDef<StatRow, unknown>[] = $derived([
\t\t{
\t\t\taccessorKey: 'cycle',
\t\t\theader: 'Cycle',
\t\t\tenableSorting: true
\t\t},
${statsColDefs}
\t]);${chartAndDataSections}${exportExcelFn}
<` + `/script>

<div class="space-y-3">
\t<!-- Toolbar -->
\t<div class="flex items-center gap-1.5 flex-wrap">
\t\t{#if TAB_DEFS.length > 1}
\t\t\t<div class={groupClass}>
\t\t\t\t{#each TAB_DEFS as tab (tab.key)}
\t\t\t\t\t<button
\t\t\t\t\t\tclass="{btnBase} {activeTab === tab.key ? btnActive : btnInactive}"
\t\t\t\t\t\tonclick={() => (activeTab = tab.key)}
\t\t\t\t\t>
\t\t\t\t\t\t{tab.label}
\t\t\t\t\t</button>
\t\t\t\t{/each}
\t\t\t</div>
\t\t\t<div class="w-px h-5 bg-border"></div>
\t\t{/if}${chartToggleUI}${excelButton}
\t</div>${chartCard}

\t<!-- Tables -->
\t{#if statRows.length > 0}
\t\t<!-- Statistics -->
\t\t<Card.Root class="gap-0 p-0 overflow-hidden">
\t\t\t<SectionHeader title="Statistics" />
\t\t\t<Card.Content class="p-2">
\t\t\t\t<DataTable
\t\t\t\t\tdata={statRows}
\t\t\t\t\tcolumns={columns}
\t\t\t\t\tshowPagination={false}
\t\t\t\t\tcompact={true}
\t\t\t\t\tenableColumnVisibility={false}
\t\t\t\t\tenableCellCopy={true}
\t\t\t\t\tgetRowId={(row) => String(row.cycle)}
\t\t\t\t/>
\t\t\t</Card.Content>
\t\t</Card.Root>${rawDataTable}
\t{/if}
</div>`;
	}

	const generatedCode = $derived.by(() => generateCode());

	// --- Copy to clipboard ---
	async function copyCode() {
		if (!generatedCode) return;
		await navigator.clipboard.writeText(generatedCode);
		copied = true;
		setTimeout(() => (copied = false), 2000);
	}
</script>

<div class="grid grid-cols-2 gap-6 max-w-[1600px]">
	<!-- Left Panel: Input & Settings -->
	<div class="space-y-4">
		<div class="flex items-center gap-2">
			<CodeIcon class="size-5 text-primary" />
			<h1 class="text-lg font-semibold">Perf Content Code Generator</h1>
		</div>

		<!-- JSON Input -->
		<Card.Root class="gap-0 p-0 overflow-hidden">
			<Card.Header class="border-b px-4 py-2 flex-row items-center justify-between">
				<Card.Title class="text-xs font-medium text-muted-foreground">JSON Sample</Card.Title>
				<div class="flex rounded-md border overflow-hidden">
					<button
						class="px-2 py-0.5 text-[10px] transition-colors {jsonPanelTab === 'text' ? 'bg-primary text-primary-foreground' : 'hover:bg-muted text-muted-foreground'}"
						onclick={() => (jsonPanelTab = 'text')}
					>Text</button>
					<button
						class="px-2 py-0.5 text-[10px] transition-colors {jsonPanelTab === 'tree' ? 'bg-primary text-primary-foreground' : 'hover:bg-muted text-muted-foreground'}"
						onclick={() => (jsonPanelTab = 'tree')}
					>Tree</button>
				</div>
			</Card.Header>
			<Card.Content class="p-2">
				{#if jsonPanelTab === 'text'}
					<textarea
						class="w-full h-48 font-mono text-xs p-3 rounded-md border bg-muted/30 resize-y focus:outline-none focus:ring-2 focus:ring-primary/50"
						placeholder={'Paste JSON here — any structure is supported'}
						bind:value={jsonInput}
					></textarea>
				{:else}
					<JsonTreeView jsonString={jsonInput} />
				{/if}
			</Card.Content>
		</Card.Root>

		<!-- Analysis Result -->
		{#if analysis.error}
			<div class="flex items-center gap-2 text-sm text-red-500">
				<AlertCircleIcon class="size-4" />
				<span>{analysis.error}</span>
			</div>
		{:else if analysis.tabs.length > 0}
			<div class="flex flex-wrap gap-2">
				<Badge variant="secondary">
					{analysis.shape} — {analysis.tabs.length} tab{analysis.tabs.length > 1 ? 's' : ''}
				</Badge>
				{#each analysis.tabs as tab}
					<Badge variant="outline">{tab.key}</Badge>
				{/each}
				{#if analysis.cycleField}
					<Badge variant="outline">cycle: {pathStr(analysis.cycleField.path)}</Badge>
				{/if}
			</div>
		{/if}

		<!-- Field Mapping -->
		{#if uniqueFields.length > 0}
			<Card.Root class="gap-0 p-0 overflow-hidden">
				<Card.Header class="border-b px-4 py-2">
					<Card.Title class="text-xs font-medium text-muted-foreground">Field Mapping</Card.Title>
				</Card.Header>
				<Card.Content class="p-0">
					<div class="overflow-auto max-h-64">
						<table class="w-full text-xs">
							<thead>
								<tr class="bg-muted/50 sticky top-0">
									<th class="px-3 py-1.5 text-left font-medium">Path</th>
									<th class="px-3 py-1.5 text-left font-medium">Type</th>
									<th class="px-3 py-1.5 text-left font-medium">Sample</th>
									<th class="px-3 py-1.5 text-left font-medium">Role</th>
								</tr>
							</thead>
							<tbody>
								{#each uniqueFields as field (pathStr(field.path))}
									<tr class="border-t hover:bg-muted/30">
										<td class="px-3 py-1.5 font-mono text-muted-foreground">{pathStr(field.path)}</td>
										<td class="px-3 py-1.5">
											<Badge variant="outline" class="text-[10px] px-1.5 py-0">
												{field.type}
											</Badge>
										</td>
										<td class="px-3 py-1.5 font-mono text-muted-foreground max-w-32 truncate">
											{sampleDisplay(field.sample)}
										</td>
										<td class="px-3 py-1.5">
											<select
												class="text-xs border rounded px-1.5 py-0.5 bg-background"
												value={field.role}
												onchange={(e) => setFieldRole(field, (e.target as HTMLSelectElement).value as FieldRole)}
											>
												<option value="tab">tab</option>
												<option value="cycle">cycle</option>
												<option value="data">data</option>
												<option value="stat">stat</option>
												<option value="ignore">ignore</option>
											</select>
										</td>
									</tr>
								{/each}
							</tbody>
						</table>
					</div>
				</Card.Content>
			</Card.Root>
		{/if}

		<!-- Settings -->
		{#if analysis.tabs.length > 0}
			<Card.Root class="gap-0 p-0 overflow-hidden">
				<Card.Header class="border-b px-4 py-2">
					<Card.Title class="text-xs font-medium text-muted-foreground">Settings</Card.Title>
				</Card.Header>
				<Card.Content class="p-4 space-y-3">
					<div class="grid grid-cols-2 gap-3">
						<div>
							<label class="text-xs text-muted-foreground mb-1 block">Component Name</label>
							<Input bind:value={componentName} class="text-xs h-8" />
						</div>
						<div>
							<label class="text-xs text-muted-foreground mb-1 block">X-axis Unit</label>
							<Input bind:value={xAxisUnit} class="text-xs h-8" />
						</div>
					</div>

					<label class="flex items-center gap-2 text-xs text-muted-foreground">
						<input type="checkbox" bind:checked={includeExcelExport} class="size-3.5" />
						Include Excel export (chart capture + Download button)
					</label>

					<!-- Tab Settings Table -->
					<div>
						<label class="text-xs text-muted-foreground mb-1 block">Tab Settings</label>
						<div class="border rounded-md overflow-hidden">
							<table class="w-full text-xs">
								<thead>
									<tr class="bg-muted/50">
										<th class="px-3 py-1.5 text-left font-medium">Key</th>
										<th class="px-3 py-1.5 text-left font-medium">Label</th>
										<th class="px-3 py-1.5 text-left font-medium">Y-axis Unit</th>
									</tr>
								</thead>
								<tbody>
									{#each analysis.tabs as tab (tab.key)}
										<tr class="border-t">
											<td class="px-3 py-1.5 text-muted-foreground">{tab.key}</td>
											<td class="px-3 py-1.5">
												<Input
													value={tabOverrides[tab.key]?.label ?? tab.label}
													oninput={(e) => {
														tabOverrides[tab.key] = {
															...tabOverrides[tab.key],
															label: (e.target as HTMLInputElement).value
														};
													}}
													class="text-xs h-7"
												/>
											</td>
											<td class="px-3 py-1.5">
												<Input
													value={tabOverrides[tab.key]?.yAxisUnit ?? tab.yAxisUnit}
													oninput={(e) => {
														tabOverrides[tab.key] = {
															...tabOverrides[tab.key],
															yAxisUnit: (e.target as HTMLInputElement).value
														};
													}}
													class="text-xs h-7"
												/>
											</td>
										</tr>
									{/each}
								</tbody>
							</table>
						</div>
					</div>
				</Card.Content>
			</Card.Root>
		{/if}
	</div>

	<!-- Right Panel: Code / Preview -->
	<div class="sticky top-14 self-start max-h-[calc(100vh-4rem)] flex flex-col">
		<Card.Root class="gap-0 p-0 overflow-hidden flex-1 flex flex-col">
			<Card.Header class="border-b px-4 py-2 flex-row items-center justify-between shrink-0">
				<div class="flex items-center gap-2">
					<div class="flex rounded-md border overflow-hidden">
						<button
							class="px-2.5 py-0.5 text-[11px] transition-colors {rightPanelTab === 'code' ? 'bg-primary text-primary-foreground' : 'hover:bg-muted text-muted-foreground'}"
							onclick={() => (rightPanelTab = 'code')}
						>
							<CodeIcon class="size-3 inline-block mr-1" />Code
						</button>
						<button
							class="px-2.5 py-0.5 text-[11px] transition-colors {rightPanelTab === 'preview' ? 'bg-primary text-primary-foreground' : 'hover:bg-muted text-muted-foreground'}"
							onclick={() => (rightPanelTab = 'preview')}
						>
							<EyeIcon class="size-3 inline-block mr-1" />Preview
						</button>
					</div>
					{#if rightPanelTab === 'code'}
						<span class="text-xs text-muted-foreground">{componentName}.svelte</span>
					{/if}
				</div>
				{#if rightPanelTab === 'code' && generatedCode}
					<Card.Action>
						<Button variant="ghost" size="sm" class="h-7 text-xs gap-1" onclick={copyCode}>
							{#if copied}
								<CheckIcon class="size-3 text-green-500" />
								Copied
							{:else}
								<CopyIcon class="size-3" />
								Copy
							{/if}
						</Button>
					</Card.Action>
				{/if}
			</Card.Header>
			<Card.Content class="p-0 flex-1 overflow-auto">
				{#if rightPanelTab === 'code'}
					{#if generatedCode}
						<pre class="bg-zinc-950 text-zinc-100 p-4 text-xs font-mono leading-relaxed overflow-auto max-h-[calc(100vh-8rem)]">{generatedCode}</pre>
					{:else if analysis.error}
						<div class="flex items-center justify-center h-48 text-muted-foreground text-sm">
							Fix JSON errors to generate code
						</div>
					{:else}
						<div class="flex items-center justify-center h-48 text-muted-foreground text-sm">
							Paste JSON sample to generate code
						</div>
					{/if}
				{:else}
					<div class="p-3">
						<PerfPreview
							{analysis}
							{mergedTabs}
							{parsedData}
							{xAxisUnit}
							{componentName}
						/>
					</div>
				{/if}
			</Card.Content>
		</Card.Root>
	</div>
</div>
