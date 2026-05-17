import { get, del, post, put } from './client.js';

export interface HealthCheck {
	name: string;
	status: 'UP' | 'DOWN' | 'TIMEOUT' | 'ERROR';
	responseTime: number;
	error?: string;
	reconnectable?: boolean;
	connectionName?: string;
	detail?: string;
}

export interface CacheEntry {
	name: string;
	group: string;
	ttl: string;
	active: boolean;
}

export interface GcStat {
	name: string;
	collectionCount: number;
	collectionTimeMs: number;
}

export interface AppInfo {
	memoryUsed: number;
	memoryMax: number;
	memoryUsedMb: number;
	memoryMaxMb: number;
	heapUsedMb: number;
	heapMaxMb: number;
	nonHeapUsedMb: number;
	javaVersion: string;
	springBootVersion: string;
	uptime: number;
	threadCount: number;
	peakThreadCount: number;
	os: string;
	processors: number;
	loadedClasses: number;
	systemLoadAverage: number;
	gc: GcStat[];
}

export interface HeadClientDetail {
	name: string;
	connected: boolean;
	slotCount: number;
	error?: string;
}

export interface PoolStat {
	name: string;
	activeConnections?: number;
	idleConnections?: number;
	totalConnections?: number;
	threadsAwaitingConnection?: number;
	maxPoolSize?: number;
}

export interface Connections {
	sseEmitters: number;
	webSocketTunnels: number;
	sshSessions: number;
	headClients: HeadClientDetail[];
	dbPools: PoolStat[];
}

export interface ScheduledTask {
	name: string;
	type: string;
	interval: string;
	description: string;
}

export interface MenuItem {
	id: string;
	title: string;
	href: string;
	visible: boolean;
}

export function fetchHealth(): Promise<HealthCheck[]> {
	return get('/admin/health');
}

export function fetchConnections(): Promise<Connections> {
	return get('/admin/connections');
}

export function fetchAppInfo(): Promise<AppInfo> {
	return get('/admin/app-info');
}

export function fetchScheduledTasks(): Promise<ScheduledTask[]> {
	return get('/admin/scheduled-tasks');
}

export function fetchCaches(): Promise<CacheEntry[]> {
	return get('/admin/caches');
}

export function clearCache(name: string): Promise<{ cleared: string }> {
	return del(`/admin/caches/${name}`);
}

export function clearAllCaches(): Promise<{ clearedCount: number }> {
	return del('/admin/caches');
}

export function fetchConfig(): Promise<Record<string, unknown>> {
	return get('/admin/config');
}

export function reconnectHead(name: string): Promise<{ reconnected: string }> {
	return post(`/admin/head/${name}/reconnect`, {});
}

export function fetchMenus(): Promise<MenuItem[]> {
	return get('/admin/menus');
}

export function updateMenus(menus: MenuItem[]): Promise<MenuItem[]> {
	return put('/admin/menus', menus);
}

// ── User management ──

export interface PortalUser {
	id: number;
	username: string;
	displayName?: string;
	role: string;
	enabled: boolean;
	createdAt?: string;
	updatedAt?: string;
}

export function fetchUsers(): Promise<PortalUser[]> {
	return get('/admin/users');
}

export function createUser(data: { username: string; password: string; displayName?: string; role?: string }): Promise<PortalUser> {
	return post('/admin/users', data);
}

export function updateUser(id: number, data: { displayName?: string; role?: string; enabled?: boolean }): Promise<PortalUser> {
	return put(`/admin/users/${id}`, data);
}

export function changeUserPassword(id: number, password: string): Promise<{ success: boolean }> {
	return put(`/admin/users/${id}/password`, { password });
}

export function deleteUser(id: number): Promise<{ success: boolean }> {
	return del(`/admin/users/${id}`);
}

// ── User permissions ──

export function fetchUserPermissions(userId: number): Promise<Record<string, boolean>> {
	return get(`/admin/users/${userId}/permissions`);
}

export function updateUserPermissions(userId: number, permissions: Record<string, boolean>): Promise<Record<string, boolean>> {
	return put(`/admin/users/${userId}/permissions`, permissions);
}

export interface PermissionDef {
	key: string;
	description: string;
}

export function fetchDefaultPermissions(): Promise<PermissionDef[]> {
	return get('/admin/permissions/defaults');
}

// ── User head access ──

export function fetchUserHeadAccess(userId: number): Promise<number[]> {
	return get(`/admin/users/${userId}/head-access`);
}

export function updateUserHeadAccess(userId: number, headConnectionIds: number[]): Promise<number[]> {
	return put(`/admin/users/${userId}/head-access`, headConnectionIds);
}

// ── Session config ──

export interface SessionConfig {
	timeoutMinutes: number;
	warnBeforeMinutes: number;
}

export function fetchSessionConfig(): Promise<SessionConfig> {
	return get('/admin/session-config');
}

export function updateSessionConfig(config: SessionConfig): Promise<SessionConfig> {
	return put('/admin/session-config', config);
}

// ── Head connection management ──

export interface HeadConnectionInfo {
	id: number;
	name: string;
	headType: number;       // 0=compatibility, 1=performance
	host: string;
	portSuffix: string;
	listenPortSuffix: string;
	port: number;
	listenPort: number;
	enabled: boolean;
	testMode: boolean;
	connected: boolean;
	createdAt?: string;
	updatedAt?: string;
}

export function fetchHeadConnections(): Promise<HeadConnectionInfo[]> {
	return get('/admin/head-connections');
}

export function createHeadConnection(data: { name: string; headType: number; host: string; portSuffix: string; listenPortSuffix: string; enabled: boolean; testMode: boolean }): Promise<HeadConnectionInfo> {
	return post('/admin/head-connections', data);
}

export function updateHeadConnection(id: number, data: { name: string; headType: number; host: string; portSuffix: string; listenPortSuffix: string; enabled: boolean; testMode: boolean }): Promise<HeadConnectionInfo> {
	return put(`/admin/head-connections/${id}`, data);
}

export function deleteHeadConnection(id: number): Promise<{ success: boolean }> {
	return del(`/admin/head-connections/${id}`);
}

export function toggleHeadConnection(id: number): Promise<HeadConnectionInfo> {
	return post(`/admin/head-connections/${id}/toggle`, {});
}

// ── Server management ──

export interface PortalServer {
	id: number;
	name: string;
	ip: string;
	username: string | null;
	password: string | null;
	sshPort: number;
	rdpPort: number;
	vncPort: number;
	connectionType: number;
	visible: boolean;
	guacdHost: string | null;
	guacdPort: number | null;
	serverGroupId: number | null;
	createdAt?: string;
	updatedAt?: string;
}

export function fetchServers(): Promise<PortalServer[]> {
	return get('/admin/servers');
}

export function createServer(data: Omit<PortalServer, 'id' | 'createdAt' | 'updatedAt'>): Promise<PortalServer> {
	return post('/admin/servers', data);
}

export function updateServer(id: number, data: Omit<PortalServer, 'id' | 'createdAt' | 'updatedAt'>): Promise<PortalServer> {
	return put(`/admin/servers/${id}`, data);
}

export function deleteServer(id: number): Promise<{ success: boolean }> {
	return del(`/admin/servers/${id}`);
}

// ── VM Status ──

export interface VmDisk {
	filesystem: string;
	size: string;
	used: string;
	avail: string;
	usePercent: string;
	mountedOn: string;
}

export interface VmStatus {
	id: number;
	name: string;
	ip: string;
	status: 'UP' | 'DOWN' | 'NO_SSH';
	error?: string;
	cpu?: { usagePercent?: number; idlePercent?: number };
	memory?: { totalMb?: number; usedMb?: number; freeMb?: number; usagePercent?: number };
	swap?: { totalMb?: number; usedMb?: number; freeMb?: number; usagePercent?: number };
	disks?: VmDisk[];
}

export function fetchVmStatus(): Promise<VmStatus[]> {
	return get('/admin/vm-status');
}

// ── Debug type/tool management ──

export interface DebugType {
	id: number;
	name: string;
	typeKey: string;
	enabled: boolean;
	description?: string;
	createdAt?: string;
	updatedAt?: string;
}

export interface DebugTool {
	id: number;
	typeId: number;
	typeName: string;
	toolName: string;
	toolPath: string;
	description?: string;
	createdAt?: string;
	updatedAt?: string;
}

export function fetchDebugTypes(): Promise<DebugType[]> {
	return get('/admin/debug-types');
}

export function createDebugType(data: { name: string; typeKey: string; enabled: boolean; description?: string }): Promise<DebugType> {
	return post('/admin/debug-types', data);
}

export function updateDebugType(id: number, data: { name: string; typeKey: string; enabled: boolean; description?: string }): Promise<DebugType> {
	return put(`/admin/debug-types/${id}`, data);
}

export function deleteDebugType(id: number): Promise<{ success: boolean }> {
	return del(`/admin/debug-types/${id}`);
}

export function fetchDebugTools(): Promise<DebugTool[]> {
	return get('/admin/debug-tools');
}

export function createDebugTool(data: { typeId: number; toolName: string; toolPath: string; description?: string }): Promise<DebugTool> {
	return post('/admin/debug-tools', data);
}

export function updateDebugTool(id: number, data: { typeId: number; toolName: string; toolPath: string; description?: string }): Promise<DebugTool> {
	return put(`/admin/debug-tools/${id}`, data);
}

export function deleteDebugTool(id: number): Promise<{ success: boolean }> {
	return del(`/admin/debug-tools/${id}`);
}

// ── DB Table management ──

export interface SetInfomation {
	id: number;
	number: number | null;
	serial: string | null;
	productName: string | null;
	modelName: string | null;
	deviceName: string | null;
	pushPath: string | null;
	osVersion: string | null;
	kernelVersion: string | null;
	vendorCommand: string | null;
	powerLevelMin: number | null;
	powerButtonClickTime: number | null;
	batteryOffStayTime: number | null;
	bootingTime: number | null;
}

export interface SlotInfomation {
	tentacleName: string;
	slotNumber: number;
	id: number | null;
	tentacleIp: string | null;
	tentacleNumber: number | null;
	slotStatus: number | null;
	testrequestId: number | null;
	numOfTestcase: number | null;
	isStart: number | null;
	currentRunningTc: number | null;
	testcaseIds: string | null;
	optional: string | null;
	testhistoryIds: string | null;
	testcaseStatus: string | null;
	testcaseInstall: string | null;
	batterLow: number | null;
	batteryHigh: number | null;
	loggingItems: string | null;
	testTypes: string | null;
	memo: string | null;
	testFinishTimes: string | null;
	porCounts: string | null;
	powerOnMins: string | null;
	powerOnMaxs: string | null;
	powerOffMins: string | null;
	powerOffMaxs: string | null;
}

export interface UfsInfoItem {
	id: number;
	name: string;
}

// Sets
export function fetchSets(): Promise<SetInfomation[]> {
	return get('/admin/db/sets');
}
export function createSet(data: Omit<SetInfomation, 'id'>): Promise<SetInfomation> {
	return post('/admin/db/sets', data);
}
export function updateSet(id: number, data: Omit<SetInfomation, 'id'>): Promise<SetInfomation> {
	return put(`/admin/db/sets/${id}`, data);
}
export function deleteSet(id: number): Promise<{ success: boolean }> {
	return del(`/admin/db/sets/${id}`);
}

// Slots
export function fetchSlots(): Promise<SlotInfomation[]> {
	return get('/admin/db/slots');
}
export function updateSlot(tentacleName: string, slotNumber: number, data: SlotInfomation): Promise<SlotInfomation> {
	return put(`/admin/db/slots/${encodeURIComponent(tentacleName)}/${slotNumber}`, data);
}

// UFS Info
export function fetchUfsInfo(table: string): Promise<UfsInfoItem[]> {
	return get(`/admin/db/ufsinfo/${table}`);
}
export function createUfsInfo(table: string, name: string): Promise<UfsInfoItem> {
	return post(`/admin/db/ufsinfo/${table}`, { name });
}
export function updateUfsInfo(table: string, id: number, name: string): Promise<UfsInfoItem> {
	return put(`/admin/db/ufsinfo/${table}/${id}`, { name });
}
export function deleteUfsInfo(table: string, id: number): Promise<{ success: boolean }> {
	return del(`/admin/db/ufsinfo/${table}/${id}`);
}

// ── Server Groups ──

export interface ServerGroup {
	id: number;
	name: string;
	description: string | null;
	sortOrder: number;
	createdAt?: string;
	updatedAt?: string;
}

export function fetchServerGroups(): Promise<ServerGroup[]> {
	return get('/admin/server-groups');
}
export function createServerGroup(data: Partial<ServerGroup>): Promise<ServerGroup> {
	return post('/admin/server-groups', data);
}
export function updateServerGroup(id: number, data: Partial<ServerGroup>): Promise<ServerGroup> {
	return put(`/admin/server-groups/${id}`, data);
}
export function deleteServerGroup(id: number): Promise<{ deleted: boolean }> {
	return del(`/admin/server-groups/${id}`);
}

// ── Permission Requests (접근 요청) ──

export interface PermissionRequestItem {
	id: number;
	userId: number;
	reason: string | null;
	status: string;
	createdAt: string;
	username?: string;
	displayName?: string;
	email?: string;
}

export function fetchPermissionRequests(): Promise<PermissionRequestItem[]> {
	return get('/admin/permission-requests');
}

export function fetchPermissionRequestCount(): Promise<{ count: number }> {
	return get('/admin/permission-requests/count');
}

export function approvePermissionRequest(
	id: number,
	permissions: Record<string, boolean>,
	headAccessIds: number[]
): Promise<{ success: boolean }> {
	return put(`/admin/permission-requests/${id}/approve`, { permissions, headAccessIds });
}

export function rejectPermissionRequest(id: number, reason: string): Promise<{ success: boolean }> {
	return put(`/admin/permission-requests/${id}/reject`, { reason });
}
