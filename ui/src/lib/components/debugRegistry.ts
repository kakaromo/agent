import type { Component } from 'svelte';
import DlmDialog from './debug/DlmDialog.svelte';
import T32DumpDialog from './debug/T32DumpDialog.svelte';
import MetadataDialog from './debug/MetadataDialog.svelte';

export interface DebugEntry {
	component: Component<any>;
	label: string;
}

/**
 * typeKey → Dialog 컴포넌트 매핑 레지스트리.
 * 새 debug type 추가 시: Dialog 구현 → 여기 등록 → DebugTypeInitializer에 등록.
 */
export const DEBUG_REGISTRY: Record<string, DebugEntry> = {
	dlm: { component: DlmDialog, label: 'DLM' },
	t32dump: { component: T32DumpDialog, label: 'T32 Dump' },
	metadata: { component: MetadataDialog, label: 'UFS Metadata' }
};
