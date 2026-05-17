// ── testdb entities ──

export interface CompatibilityTestRequest {
	id: number;
	name: string;
	product: string;
	controller: string;
	specVersion: string;
	cellType: string;
	nandType: string;
	nandSize: string;
	density: string;
	fwVersion: string;
	isReleasedFw: string;
	date: string;
	description?: string;
	engineer?: string;
	testType?: string;
	fw?: string;
}

export interface CompatibilityHistory {
	id: number;
	result?: string;
	failCause?: string;
	startTime?: string;
	endTime?: string;
	runningTime?: string;
	setSerial?: string;
	setProductName?: string;
	setModelName?: string;
	setDeviceName?: string;
	tentacleIp?: string;
	usbId?: string;
	logPath?: string;
	tcId?: number;
	optional?: string;
	trId?: number;
	slotLocation?: string;
	testType?: string;
	porCount?: number;
	testExpectFinishTime?: number;
	dsplmUpload?: string;
	powerOnMin?: number;
	powerOnMax?: number;
	powerOffMin?: number;
	powerOffMax?: number;
}

export interface CompatibilityTestCase {
	id: number;
	name?: string;
	fileName?: string;
	tcOption?: string;
	version?: string;
	type?: string;
	testType?: string;
	author?: string;
	date?: string;
	hidden?: number;
	belongTo?: string;
	density?: string;
	specific?: string;
}

export interface PerformanceTestRequest {
	id: number;
	controller: string;
	specVersion: string;
	cellType: string;
	nandType: string;
	nandSize: string;
	density: string;
	fwVersion: string;
	baseFwVersion: string;
	oem: string;
	csVersion?: number;
	date: string;
	fw?: string;
}

export interface Oem {
	id: number;
	name: string;
}

export interface PerformanceHistory {
	id: number;
	startTime?: string;
	endTime?: string;
	runningTime?: string;
	setId?: number;
	logPath?: string;
	tcId?: string;
	trId?: string;
	slotLocation?: string;
	uploaded?: string;
	result?: string;
}

export interface PerformanceTestCase {
	id: number;
	name?: string;
	fileName?: string;
	parserId?: number;
	author?: string;
	date?: string;
	hidden?: number;
	category?: string;
	ioType?: string;
	tcOption?: string;
}

export interface PerformanceParser {
	id: number;
	name: string;
}

export interface SetInfomation {
	id: number;
	number?: number;
	serial?: string;
	productName?: string;
	modelName?: string;
	deviceName?: string;
	pushPath?: string;
	osVersion?: string;
	kernelVersion?: string;
	vendorCommand?: string;
	powerLevelMin?: number;
	powerButtonClickTime?: number;
	batteryOffStayTime?: number;
	bootingTime?: number;
}

export interface SlotInfomation {
	id: number;
	tentacleIp?: string;
	tentacleNumber?: number;
	tentacleName?: string;
	slotNumber?: number;
	slotStatus?: number;
	testrequestId?: number;
	numOfTestcase?: number;
	isStart?: number;
	currentRunningTc?: number;
	testcaseIds?: string;
	optional?: string;
	testhistoryIds?: string;
	testcaseStatus?: string;
	testcaseInstall?: string;
	batterLow?: number;
	batteryHigh?: number;
	loggingItems?: string;
	testTypes?: string;
	memo?: string;
	testFinishTimes?: string;
	porCounts?: string;
	powerOnMins?: string;
	powerOnMaxs?: string;
	powerOffMins?: string;
	powerOffMaxs?: string;
}

// ── UFSInfo entities ──

export interface CellType {
	id: number;
	name: string;
}

export interface Controller {
	id: number;
	name: string;
}

export interface Density {
	id: number;
	name: string;
}

export interface NandSize {
	id: number;
	name: string;
}

export interface NandType {
	id: number;
	name: string;
}

export interface UfsVersion {
	id: number;
	name: string;
}

// ── Pagination ──

// Spring Data VIA_DTO (PagedModel) 응답 구조
export interface Page<T> {
	content: T[];
	page: {
		totalElements: number;
		totalPages: number;
		number: number;
		size: number;
	};
}

export interface GroupCount {
	groupKey: string;
	groupValue: string;
	count: number;
}

// ── Dashboard stats ──

export interface FwSummary {
	trId: number;
	fw: string;
	pass: number;
	fail: number;
	total: number;
	rate: number;
}

export interface TcSummary {
	tcId: number;
	tc: string;
	pass: number;
	fail: number;
	total: number;
	rate: number;
}

export interface RecentCompatHistory {
	id: number;
	result: string | null;
	trId: number | null;
	tcId: number | null;
	setProductName: string | null;
	trName: string | null;
	trFw: string | null;
	tcName: string | null;
}

export interface RecentPerfHistory {
	id: number;
	result: string | null;
	trId: number | null;
	tcId: number | null;
	setId: number | null;
	trFw: string | null;
	tcName: string | null;
	setName: string | null;
}

export interface DashboardCategoryStats {
	trCount: number;
	tcCount: number;
	totalCount: number;
	passCount: number;
	failCount: number;
	byFw: FwSummary[];
	byTc: TcSummary[];
	recent: RecentCompatHistory[] | RecentPerfHistory[];
}

export interface DashboardStats {
	compatibility: DashboardCategoryStats;
	performance: DashboardCategoryStats;
}
