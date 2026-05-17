import { get, post, put, patch, del } from './client.js';
import type {
	CompatibilityTestRequest,
	CompatibilityHistory,
	CompatibilityTestCase,
	PerformanceTestRequest,
	PerformanceHistory,
	PerformanceTestCase,
	PerformanceParser,
	SetInfomation,
	SlotInfomation,
	Page,
	GroupCount,
	DashboardStats
} from './types.js';

// Dashboard
export const fetchDashboardStats = () =>
	get<DashboardStats>('/dashboard/stats');

// Compatibility Test Requests
export const fetchCompatibilityTestRequests = () =>
	get<CompatibilityTestRequest[]>('/compatibility-test-requests');

export const createCompatibilityTestRequest = (data: Partial<CompatibilityTestRequest>) =>
	post<CompatibilityTestRequest>('/compatibility-test-requests', data);

export const updateCompatibilityTestRequest = (id: number, data: Partial<CompatibilityTestRequest>) =>
	put<CompatibilityTestRequest>(`/compatibility-test-requests/${id}`, data);

export const deleteCompatibilityTestRequest = (id: number) =>
	del(`/compatibility-test-requests/${id}`);

// Compatibility Histories
export const fetchCompatibilityHistories = () =>
	get<CompatibilityHistory[]>('/compatibility-histories');

export const fetchCompatibilityHistoryPage = (page: number, size: number, sort = 'id,desc') =>
	get<Page<CompatibilityHistory>>(`/compatibility-histories/page?page=${page}&size=${size}&sort=${sort}`);

export const fetchCompatibilityHistoryGroupCounts = (groupBy: string) =>
	get<GroupCount[]>(`/compatibility-histories/group-by/${groupBy}`);

export const fetchCompatibilityHistoryByGroup = (groupBy: string, groupValue: string, page: number, size: number, sort = 'id,desc') =>
	get<Page<CompatibilityHistory>>(`/compatibility-histories/group-by/${groupBy}/${groupValue}?page=${page}&size=${size}&sort=${sort}`);

export const fetchCompatibilityHistory = (id: number) =>
	get<CompatibilityHistory>(`/compatibility-histories/${id}`);

export const fetchCompatibilityHistoryTcGroupsByTr = (trId: string) =>
	get<GroupCount[]>(`/compatibility-histories/by-tr/${trId}/tc-groups`);

export const fetchCompatibilityHistoryByTrAndTc = (trId: string, tcId: string, page: number, size: number, sort = 'id,desc') =>
	get<Page<CompatibilityHistory>>(`/compatibility-histories/by-tr/${trId}/tc/${tcId}?page=${page}&size=${size}&sort=${sort}`);

// Compatibility Test Cases
export const fetchCompatibilityTestCases = () =>
	get<CompatibilityTestCase[]>('/compatibility-test-cases');

export const createCompatibilityTestCase = (data: Partial<CompatibilityTestCase>) =>
	post<CompatibilityTestCase>('/compatibility-test-cases', data);

export const updateCompatibilityTestCase = (id: number, data: Partial<CompatibilityTestCase>) =>
	put<CompatibilityTestCase>(`/compatibility-test-cases/${id}`, data);

export const deleteCompatibilityTestCase = (id: number) =>
	del(`/compatibility-test-cases/${id}`);

// Performance Test Requests
export const fetchPerformanceTestRequests = () =>
	get<PerformanceTestRequest[]>('/performance-test-requests');

export const createPerformanceTestRequest = (data: Partial<PerformanceTestRequest>) =>
	post<PerformanceTestRequest>('/performance-test-requests', data);

export const updatePerformanceTestRequest = (id: number, data: Partial<PerformanceTestRequest>) =>
	put<PerformanceTestRequest>(`/performance-test-requests/${id}`, data);

export const deletePerformanceTestRequest = (id: number) =>
	del(`/performance-test-requests/${id}`);

// Performance Histories
export const fetchPerformanceHistories = () =>
	get<PerformanceHistory[]>('/performance-histories');

export const fetchPerformanceHistoryPage = (page: number, size: number, sort = 'id,desc', startDate?: string, endDate?: string) => {
	let url = `/performance-histories/page?page=${page}&size=${size}&sort=${sort}`;
	if (startDate && endDate) url += `&startDate=${startDate}&endDate=${endDate}`;
	return get<Page<PerformanceHistory>>(url);
};

export const fetchPerformanceDistinctResults = () =>
	get<string[]>('/performance-histories/distinct-results');

export const fetchCompatibilityDistinctResults = () =>
	get<string[]>('/compatibility-histories/distinct-results');

export const fetchPerformanceHistoryGroupCounts = (groupBy: string) =>
	get<GroupCount[]>(`/performance-histories/group-by/${groupBy}`);

export const fetchPerformanceHistoryByGroup = (groupBy: string, groupValue: string, page: number, size: number, sort = 'id,desc') =>
	get<Page<PerformanceHistory>>(`/performance-histories/group-by/${groupBy}/${groupValue}?page=${page}&size=${size}&sort=${sort}`);

export const fetchPerformanceHistory = (id: number) =>
	get<PerformanceHistory>(`/performance-histories/${id}`);

export const fetchPerformanceHistoriesByIds = (ids: number[]) => {
	if (ids.length === 0) return Promise.resolve([]);
	return get<PerformanceHistory[]>(`/performance-histories/by-ids?${ids.map(id => `ids=${id}`).join('&')}`);
};

export const fetchPerformanceHistoryTcGroupsByTr = (trId: string) =>
	get<GroupCount[]>(`/performance-histories/by-tr/${trId}/tc-groups`);

export const fetchPerformanceHistoryByTrAndTc = (trId: string, tcId: string, page: number, size: number, sort = 'id,desc') =>
	get<Page<PerformanceHistory>>(`/performance-histories/by-tr/${trId}/tc/${tcId}?page=${page}&size=${size}&sort=${sort}`);

// Performance History Search
export interface HistorySearchParams {
	result?: string;
	slotLocation?: string;
	trId?: number;
	tcId?: number;
	tcIds?: number[];
	startDate?: string;
	endDate?: string;
	setModelName?: string;
	testType?: string;
	trIds?: number[];
}

function buildSearchQuery(params: HistorySearchParams, page: number, size: number, sort: string): URLSearchParams {
	const query = new URLSearchParams();
	query.set('page', String(page));
	query.set('size', String(size));
	query.set('sort', sort);
	Object.entries(params).forEach(([k, v]) => {
		if (v == null || v === '') return;
		if ((k === 'trIds' || k === 'tcIds') && Array.isArray(v)) {
			for (const id of v) query.append(k, String(id));
		} else {
			query.set(k, String(v));
		}
	});
	return query;
}

export const searchPerformanceHistory = (params: HistorySearchParams, page: number, size: number, sort = 'id,desc') =>
	get<Page<PerformanceHistory>>(`/performance-histories/search?${buildSearchQuery(params, page, size, sort)}`);

export const searchCompatibilityHistory = (params: HistorySearchParams, page: number, size: number, sort = 'id,desc') =>
	get<Page<CompatibilityHistory>>(`/compatibility-histories/search?${buildSearchQuery(params, page, size, sort)}`);

// Performance Test Cases
export const fetchPerformanceTestCases = () =>
	get<PerformanceTestCase[]>('/performance-test-cases');

export const createPerformanceTestCase = (data: Partial<PerformanceTestCase>) =>
	post<PerformanceTestCase>('/performance-test-cases', data);

export const updatePerformanceTestCase = (id: number, data: Partial<PerformanceTestCase>) =>
	put<PerformanceTestCase>(`/performance-test-cases/${id}`, data);

export const deletePerformanceTestCase = (id: number) =>
	del(`/performance-test-cases/${id}`);

// Performance Parsers
export const fetchPerformanceParsers = () =>
	get<PerformanceParser[]>('/performance-parsers');

export const createPerformanceParser = (data: Partial<PerformanceParser>) =>
	post<PerformanceParser>('/performance-parsers', data);

export const updatePerformanceParser = (id: number, data: Partial<PerformanceParser>) =>
	put<PerformanceParser>(`/performance-parsers/${id}`, data);

export const deletePerformanceParser = (id: number) =>
	del(`/performance-parsers/${id}`);

// Set Information
export const fetchSetInfomations = () =>
	get<SetInfomation[]>('/set-infomations');

// Slot Information
export const fetchSlotInfomations = () =>
	get<SlotInfomation[]>('/slot-infomations');

export const fetchSlotInfomation = (tentacleName: string, slotNumber: number) =>
	get<SlotInfomation>(`/slot-infomations/${tentacleName}/${slotNumber}`);

export const createSlotInfomation = (data: Partial<SlotInfomation>) =>
	post<SlotInfomation>('/slot-infomations', data);

export const updateSlotInfomation = (tentacleName: string, slotNumber: number, data: Partial<SlotInfomation>) =>
	put<SlotInfomation>(`/slot-infomations/${tentacleName}/${slotNumber}`, data);

export const deleteSlotInfomation = (tentacleName: string, slotNumber: number) =>
	del(`/slot-infomations/${tentacleName}/${slotNumber}`);

export const updateSlotMemo = (tentacleName: string, slotNumber: number, memo: string) =>
	patch<void>(`/slot-infomations/${tentacleName}/${slotNumber}/memo`, { memo });

// Performance Results
export interface PerformanceResultData {
	parserId: number;
	parserName: string;
	tcName: string;
	fw?: string;
	fileSystem?: string;
	data: unknown;
	partial?: boolean;
	status?: 'collecting';
	message?: string;
}

export const fetchPerformanceResultData = (historyId: number) =>
	get<PerformanceResultData>(`/performance-results/${historyId}/data`);

export const fetchPerformanceResultDataBatch = (ids: number[]) =>
	Promise.all(ids.map((id) => fetchPerformanceResultData(id)));

// TC Groups
export interface TcGroupItem {
	id: number;
	tcId: number;
	sortOrder: number;
}

export interface TcGroup {
	id: number;
	name: string;
	tcType: string;
	description?: string;
	items: TcGroupItem[];
	createdAt: string;
	updatedAt: string;
}

export interface TcGroupRequest {
	name: string;
	tcType: string;
	description?: string;
	tcIds: number[];
}

export const fetchTcGroups = (type?: string) =>
	get<TcGroup[]>(type ? `/tc-groups?type=${type}` : '/tc-groups');

export const createTcGroup = (data: TcGroupRequest) =>
	post<TcGroup>('/tc-groups', data);

export const updateTcGroup = (id: number, data: TcGroupRequest) =>
	put<TcGroup>(`/tc-groups/${id}`, data);

export const deleteTcGroup = (id: number) =>
	del(`/tc-groups/${id}`);

// MakeSet Groups
export interface MakesetGroupItem {
	id?: number;
	board: string;
	provisionPath: string;
	imagePath: string;
	ddValue: string;
}

export interface MakesetGroup {
	id: number;
	name: string;
	description?: string;
	items: MakesetGroupItem[];
	createdAt: string;
	updatedAt: string;
}

export interface MakesetGroupRequest {
	name: string;
	description?: string;
	items: MakesetGroupItem[];
}

export const fetchMakesetGroups = () =>
	get<MakesetGroup[]>('/makeset-groups');

export const createMakesetGroup = (data: MakesetGroupRequest) =>
	post<MakesetGroup>('/makeset-groups', data);

export const updateMakesetGroup = (id: number, data: MakesetGroupRequest) =>
	put<MakesetGroup>(`/makeset-groups/${id}`, data);

export const deleteMakesetGroup = (id: number) =>
	del(`/makeset-groups/${id}`);

// Head commands
export interface HeadCommandRequest {
	source: string;
	command: string;
	slotNumbers: number[];
	data: string;
	newOrder?: number;
	currentOrder?: number;
	orders?: number[];
}

export const sendHeadCommand = (req: HeadCommandRequest) =>
	post<{ success: boolean; message: string }>('/head/command', req);

// Head connections list
export interface HeadConnectionStatus {
	name: string;
	headType: number;       // 0=compatibility, 1=performance
	connected: boolean;
	testMode: boolean;
	error?: string;
}

export const fetchHeadConnectionList = () =>
	get<HeadConnectionStatus[]>('/head/connections');

// Head reconnect
export const reconnectHead = (source: string) =>
	post<{ success: boolean; message: string }>(`/head/reconnect/${source}`, {});

export const reconnectAllHead = () =>
	post<{ success: boolean; message: string }>('/head/reconnect', {});

// MakeSet boards & DD options
export const fetchMakesetBoards = () =>
	get<string[]>('/head/makeset/boards');

export interface DdOption {
	name: string;
	enabled: boolean;
}

export const fetchDdOptions = (source: string, board: string) =>
	get<DdOption[]>(`/head/makeset/dd-options?source=${encodeURIComponent(source)}&board=${encodeURIComponent(board)}`);
