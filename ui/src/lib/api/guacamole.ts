import { get } from './client.js';

export interface VmInfo {
	name: string;
	ip: string;
	sshPort: number;
	rdpPort: number;
	vncPort: number;
	connectionType: number; // bitmask: 1=SSH, 2=RDP, 4=VNC
	serverGroupId: number | null;
	serverGroupName: string | null;
}

// connectionType bitmask helpers
export const CONN_SSH = 1;
export const CONN_RDP = 2;
export const CONN_VNC = 4;
export function hasSSH(vm: VmInfo): boolean { return (vm.connectionType & CONN_SSH) !== 0; }
export function hasRDP(vm: VmInfo): boolean { return (vm.connectionType & CONN_RDP) !== 0; }
export function hasVNC(vm: VmInfo): boolean { return (vm.connectionType & CONN_VNC) !== 0; }

export function fetchVms(): Promise<VmInfo[]> {
	return get<VmInfo[]>('/guacamole/vms');
}

export function fetchViewerCounts(): Promise<Record<string, number>> {
	return get<Record<string, number>>('/guacamole/viewers');
}

// ── Session Lock ──

export interface SessionLock {
	locked: boolean;
	user?: string;
	protocol?: string;
	lockedAt?: string;
}

export function fetchSessionLocks(): Promise<Record<string, { user: string; protocol: string; lockedAt: string }>> {
	return get('/guacamole/session-locks');
}

export function fetchSessionLock(vm: string): Promise<SessionLock> {
	return get(`/guacamole/session-locks/${encodeURIComponent(vm)}`);
}

export function pollSessionAttempts(vm: string): Promise<{ user: string; attemptedAt: string }[]> {
	return get(`/guacamole/session-locks/${encodeURIComponent(vm)}/attempts`);
}
