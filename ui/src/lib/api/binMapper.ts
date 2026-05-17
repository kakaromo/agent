import { get, del } from './client.js';
import { getCsrfToken } from '$lib/stores/auth.svelte.js';

export interface MappedField {
	fieldName: string;
	typeName: string;
	offset: number;
	size: number;
	hexBytes: string;
	parsedValue: unknown;
	children?: MappedField[];
}

export interface MappedInstance {
	index: number;
	offset: number;
	fields: MappedField[];
}

export interface MappedResult {
	structName: string;
	structSize: number;
	totalBytes: number;
	endianness: string;
	instanceCount: number;
	instances: MappedInstance[];
	hexDump: string;
	rawBytes: number[];
}

export interface StructInfo {
	name: string;
	fields: unknown[];
	packed: boolean;
}

export type PredefinedStructKind = 'metadata' | 'dlm' | 'general';

export interface PredefinedStruct {
	id: number;
	name: string;
	category: string;
	kind: PredefinedStructKind;
	structText: string;
	description: string;
	createdAt: string;
	updatedAt: string;
}

export async function parseBinary(params: {
	binaryFile?: File;
	serverPath?: string;
	serverName?: string;
	structText?: string;
	structFile?: File;
	predefinedStructId?: number;
	structName?: string;
	endianness?: string;
	repeatAsArray?: boolean;
}): Promise<MappedResult> {
	const formData = new FormData();
	if (params.binaryFile) formData.append('binaryFile', params.binaryFile);
	if (params.serverPath) formData.append('serverPath', params.serverPath);
	if (params.serverName) formData.append('serverName', params.serverName);
	if (params.structText) formData.append('structText', params.structText);
	if (params.structFile) formData.append('structFile', params.structFile);
	if (params.predefinedStructId != null)
		formData.append('predefinedStructId', String(params.predefinedStructId));
	if (params.structName) formData.append('structName', params.structName);
	formData.append('endianness', params.endianness ?? 'AUTO');
	formData.append('repeatAsArray', String(params.repeatAsArray ?? false));

	const res = await fetch('/api/binmapper/parse', {
		method: 'POST',
		headers: { 'X-XSRF-TOKEN': getCsrfToken() },
		body: formData
	});

	if (!res.ok) {
		const err = await res.json().catch(() => ({ error: res.statusText }));
		throw new Error(err.error || `Parse failed (${res.status})`);
	}
	return res.json();
}

export async function parseStructOnly(structText: string): Promise<StructInfo[]> {
	const res = await fetch('/api/binmapper/parse-struct', {
		method: 'POST',
		headers: {
			'Content-Type': 'application/json',
			'X-XSRF-TOKEN': getCsrfToken()
		},
		body: JSON.stringify({ structText })
	});
	if (!res.ok) {
		const err = await res.json().catch(() => ({ error: res.statusText }));
		throw new Error(err.error || 'Parse failed');
	}
	return res.json();
}

export async function parseHeader(file: File): Promise<StructInfo[]> {
	const formData = new FormData();
	formData.append('file', file);
	const res = await fetch('/api/binmapper/parse-header', {
		method: 'POST',
		headers: { 'X-XSRF-TOKEN': getCsrfToken() },
		body: formData
	});
	if (!res.ok) {
		const err = await res.json().catch(() => ({ error: res.statusText }));
		throw new Error(err.error || 'Header parse failed');
	}
	return res.json();
}

export function getStructs(kind?: PredefinedStructKind): Promise<PredefinedStruct[]> {
	const qs = kind ? `?kind=${encodeURIComponent(kind)}` : '';
	return get<PredefinedStruct[]>(`/binmapper/structs${qs}`);
}

export function deleteStruct(id: number): Promise<void> {
	return del(`/binmapper/structs/${id}`);
}

export interface StructPayload {
	name: string;
	category: string;
	kind?: PredefinedStructKind;
	structText: string;
	description: string;
}

export async function createStruct(data: StructPayload): Promise<PredefinedStruct> {
	const res = await fetch('/api/binmapper/structs', {
		method: 'POST',
		headers: {
			'Content-Type': 'application/json',
			'X-XSRF-TOKEN': getCsrfToken()
		},
		body: JSON.stringify(data)
	});
	return res.json();
}

export async function updateStruct(id: number, data: StructPayload): Promise<PredefinedStruct> {
	const res = await fetch(`/api/binmapper/structs/${id}`, {
		method: 'PUT',
		headers: {
			'Content-Type': 'application/json',
			'X-XSRF-TOKEN': getCsrfToken()
		},
		body: JSON.stringify(data)
	});
	return res.json();
}
