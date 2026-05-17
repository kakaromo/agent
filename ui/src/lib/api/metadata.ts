import { get, post, put, del } from './client.js';

// --- Types ---

export interface MetadataType {
	id: number;
	name: string;
	typeKey: string;
	category: string;
	enabled: boolean;
	description: string | null;
}

export type MetadataCommandType =
	| 'tool'
	| 'sysfs'
	| 'keyvalue'
	| 'raw'
	| 'table'
	| 'bitmap'
	| 'binary';

export interface MetadataCommand {
	id: number;
	metadataType: MetadataType;
	commandTemplate: string;
	commandType: MetadataCommandType;
	debugTool: { id: number; toolName: string; toolPath: string } | null;
	description: string | null;
	predefinedStruct?: {
		id: number;
		name: string;
		kind: string;
		category: string | null;
	} | null;
	binaryOutputPath?: string | null;
	binaryEndianness?: string | null;
}

export interface ProductMapping {
	id: number;
	controller: string | null;
	cellType: string | null;
	nandType: string | null;
	oem: string | null;
	metadataTypeIds: string; // CSV "1,3,5"
}

export interface MetadataEntry {
	time: number;
	[key: string]: any;
}

export interface MetadataStatus {
	slotKey: string;
	tentacleName: string;
	slotNumber: number;
	testToolName: string;
	types: string[];
	elapsedSeconds: number;
}

export interface SlotMetadataStatus {
	monitoring: boolean;
	testToolName?: string;
	startTimeMs?: number;
	elapsedSeconds?: number;
	types?: string[];
	entryCounts?: Record<string, number>;
}

// --- Data API ---

export function fetchMetadataTypes(): Promise<MetadataType[]> {
	return get('/metadata/types');
}

export function fetchMetadataForProduct(
	controller?: string,
	nandType?: string,
	cellType?: string
): Promise<MetadataType[]> {
	const params = new URLSearchParams();
	if (controller) params.set('controller', controller);
	if (nandType) params.set('nandType', nandType);
	if (cellType) params.set('cellType', cellType);
	return get(`/metadata/types/for-product?${params}`);
}

/** TR id 기반 metadata 타입 조회 — headType: 0=compatibility, 1=performance */
export function fetchMetadataForTr(trId: number, headType: number): Promise<MetadataType[]> {
	const params = new URLSearchParams();
	params.set('trId', String(trId));
	params.set('headType', String(headType));
	return get(`/metadata/types/for-tr?${params}`);
}

export function fetchSlotMetadataStatus(
	tentacleName: string,
	slotNumber: number
): Promise<SlotMetadataStatus> {
	return get(`/metadata/slot/${tentacleName}/${slotNumber}`);
}

export function fetchSlotMetadata(
	tentacleName: string,
	slotNumber: number,
	typeKey: string
): Promise<MetadataEntry[]> {
	return get(`/metadata/slot/${tentacleName}/${slotNumber}/${typeKey}`);
}

export function fetchMetadataFile(
	tentacleName: string,
	path: string,
	tentacleIp?: string
): Promise<string> {
	const params = new URLSearchParams({ tentacleName, path });
	if (tentacleIp) params.set('tentacleIp', tentacleIp);
	return get(`/metadata/file?${params}`);
}

export function fetchSlotMetadataFiles(
	tentacleName: string,
	slotNumber: number,
	logPath?: string,
	tentacleIp?: string
): Promise<string> {
	const params = new URLSearchParams();
	if (logPath) params.set('logPath', logPath);
	if (tentacleIp) params.set('tentacleIp', tentacleIp);
	const qs = params.toString() ? `?${params}` : '';
	return get(`/metadata/slot/${tentacleName}/${slotNumber}/files${qs}`);
}

export function fetchMonitoringStatus(): Promise<MetadataStatus[]> {
	return get('/metadata/status');
}

export function updateMetadataConfig(config: {
	collectionIntervalMin?: number;
	enabled?: boolean;
}): Promise<{ enabled: boolean; collectionIntervalMin: number }> {
	return put('/metadata/config', config);
}

// --- Slot toggle ---

export function fetchSlotMetadataEnabled(
	tentacleName: string,
	slotNumber: number
): Promise<{ enabled: boolean }> {
	return get(`/metadata/slot/${tentacleName}/${slotNumber}/enabled`);
}

export function setSlotMetadataEnabled(
	tentacleName: string,
	slotNumber: number,
	enabled: boolean
): Promise<{ enabled: boolean }> {
	return put(`/metadata/slot/${tentacleName}/${slotNumber}/enabled`, { enabled });
}

export function fetchExcludedTypes(
	tentacleName: string,
	slotNumber: number
): Promise<{ excludedTypes: string[] }> {
	return get(`/metadata/slot/${tentacleName}/${slotNumber}/excluded-types`);
}

export function setExcludedTypes(
	tentacleName: string,
	slotNumber: number,
	excludedTypes: string[]
): Promise<{ excludedTypes: string[] }> {
	return put(`/metadata/slot/${tentacleName}/${slotNumber}/excluded-types`, { excludedTypes });
}

export function fetchSlotInterval(
	tentacleName: string,
	slotNumber: number
): Promise<{ intervalSeconds: number; defaultIntervalSeconds: number }> {
	return get(`/metadata/slot/${tentacleName}/${slotNumber}/interval`);
}

export function setSlotInterval(
	tentacleName: string,
	slotNumber: number,
	intervalSeconds: number
): Promise<{ intervalSeconds: number }> {
	return put(`/metadata/slot/${tentacleName}/${slotNumber}/interval`, { intervalSeconds });
}

// --- Admin API ---

export function fetchAllMetadataTypes(): Promise<MetadataType[]> {
	return get('/admin/metadata/types');
}

export function createMetadataType(type: Partial<MetadataType>): Promise<MetadataType> {
	return post('/admin/metadata/types', type);
}

export function updateMetadataType(id: number, type: Partial<MetadataType>): Promise<MetadataType> {
	return put(`/admin/metadata/types/${id}`, type);
}

export function deleteMetadataType(id: number): Promise<void> {
	return del(`/admin/metadata/types/${id}`);
}

export function fetchAllCommands(): Promise<MetadataCommand[]> {
	return get('/admin/metadata/commands');
}

export function fetchCommandsByType(typeId: number): Promise<MetadataCommand[]> {
	return get(`/admin/metadata/commands/by-type/${typeId}`);
}

export interface CommandPayload {
	metadataTypeId?: number;
	commandTemplate: string;
	commandType?: string;
	debugToolId?: number | null;
	description?: string;
	predefinedStructId?: number | null;
	binaryOutputPath?: string | null;
	binaryEndianness?: string | null;
}

export function createCommand(data: CommandPayload & { metadataTypeId: number }): Promise<MetadataCommand> {
	return post('/admin/metadata/commands', data);
}

export function updateCommand(id: number, data: CommandPayload): Promise<MetadataCommand> {
	return put(`/admin/metadata/commands/${id}`, data);
}

export function deleteCommand(id: number): Promise<void> {
	return del(`/admin/metadata/commands/${id}`);
}

export function fetchAllProductMappings(): Promise<ProductMapping[]> {
	return get('/admin/metadata/product-mappings');
}

export function createProductMapping(data: {
	controller?: string;
	cellType?: string;
	nandType?: string;
	oem?: string;
	metadataTypeIds: number[];
}): Promise<ProductMapping> {
	return post('/admin/metadata/product-mappings', data);
}

export function updateProductMapping(id: number, data: {
	controller?: string;
	cellType?: string;
	nandType?: string;
	oem?: string;
	metadataTypeIds: number[];
}): Promise<ProductMapping> {
	return put(`/admin/metadata/product-mappings/${id}`, data);
}

export function deleteProductMapping(id: number): Promise<void> {
	return del(`/admin/metadata/product-mappings/${id}`);
}
