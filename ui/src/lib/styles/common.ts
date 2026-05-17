// ── 버튼 ──

/** 아이콘 + 텍스트 작은 버튼 (Refresh, Add 등) */
export const btnIcon = 'inline-flex items-center gap-1 rounded-md border px-2.5 py-1 text-[10px] hover:bg-muted transition-colors disabled:opacity-50';

/** 아이콘 + 텍스트 작은 버튼 (Primary 강조) */
export const btnIconPrimary = 'inline-flex items-center gap-1 rounded-md border border-primary/30 bg-primary/10 px-2.5 py-1 text-[10px] text-primary hover:bg-primary/20 transition-colors';

/** 정사각형 아이콘 버튼 (Edit, Delete 등 액션) */
export const btnSquare = 'inline-flex items-center rounded border p-1 text-muted-foreground hover:text-foreground hover:bg-muted transition-colors';

/** 정사각형 아이콘 버튼 (Delete 위험 강조) */
export const btnSquareDanger = 'inline-flex items-center rounded border p-1 text-muted-foreground hover:text-red-600 hover:border-red-200 transition-colors';

// ── 텍스트 ──

/** 캡션 (회색 작은 텍스트) */
export const captionMuted = 'text-[10px] text-muted-foreground';

/** 섹션 라벨 (대문자 굵은 캡션) */
export const sectionLabel = 'text-[10px] font-medium text-muted-foreground uppercase tracking-wider';

/** 폼 필드 라벨 */
export const fieldLabel = 'text-[10px] text-muted-foreground block mb-1';

/** 작은 텍스트 버튼 (탭, 필터 등) */
export const btnXs = 'flex items-center gap-1 px-2 py-1 text-xs rounded border hover:bg-muted transition-colors';

/** 태그/칩 (muted 배경) */
export const tagMuted = 'inline-flex items-center gap-1 rounded-md bg-muted px-2 py-0.5 text-[10px]';

// ── 인풋 ──

/** 작은 인풋 (Agent 옵션 등 h-6) */
export const inputSm = 'w-full h-6 px-1.5 text-[10px] rounded border bg-background';

/** 기본 인풋 (px-2.5 py-1.5) */
export const inputBase = 'w-full border rounded px-2.5 py-1.5 text-xs bg-background';

// ── 카드/레이아웃 ──

/** 카드 행 (사용자 목록, 서버 목록 등) */
export const cardRow = 'flex items-center justify-between rounded-lg border bg-card px-3 py-2.5 transition-colors';

// ── 배지 ──

/** 배지 기본 */
export const badge = 'text-[10px] px-1.5 py-0.5 rounded-full';

/** 배지 — 성공 */
export const badgeSuccess = `${badge} bg-green-100 text-green-700`;

/** 배지 — 위험 */
export const badgeDanger = `${badge} bg-red-100 text-red-700`;

/** 배지 — 경고 */
export const badgeWarning = `${badge} bg-amber-100 text-amber-700`;

/** 배지 — 비활성 */
export const badgeMuted = `${badge} bg-muted text-muted-foreground`;

// ── 상태 ──

/** 빈 상태 (데이터 없음 안내) */
export const emptyState = 'flex flex-col items-center justify-center py-12 text-muted-foreground gap-1';
