<script lang="ts">
	import {
		getStructs, parseHeader, createStruct, updateStruct, deleteStruct,
		type PredefinedStruct, type StructInfo
	} from '$lib/api/binMapper.js';

	let {
		structText = $bindable(''),
		structFile = $bindable<File | null>(null),
		predefinedStructId = $bindable<number | null>(null),
		structName = $bindable('')
	} = $props();

	let mode = $state<'text' | 'file' | 'predefined'>('text');
	let predefinedList = $state<PredefinedStruct[]>([]);
	let headerStructs = $state<StructInfo[]>([]);
	let parseError = $state('');
	let loading = $state(false);

	// ── Predefined CRUD state ──
	let pdMode = $state<'list' | 'create' | 'edit'>('list');
	let pdForm = $state({ name: '', category: '', kind: 'general' as 'metadata' | 'dlm' | 'general', description: '', structText: '' });
	let pdEditId = $state<number | null>(null);
	let pdSaving = $state(false);
	let pdError = $state('');
	let pdInputMode = $state<'paste' | 'file'>('paste');
	let pdHeaderStructs = $state<StructInfo[]>([]);
	let pdHeaderParsing = $state(false);
	let pdHeaderError = $state('');
	let pdDeleteConfirm = $state<number | null>(null);

	async function loadPredefined() {
		try {
			predefinedList = await getStructs();
		} catch (e) {
			console.error('Failed to load predefined structs', e);
		}
	}

	async function handleHeaderFile(e: Event) {
		const input = e.target as HTMLInputElement;
		const file = input.files?.[0];
		if (!file) return;
		structFile = file;
		parseError = '';
		loading = true;
		try {
			headerStructs = await parseHeader(file);
			if (headerStructs.length === 1) {
				structName = headerStructs[0].name || '';
			}
		} catch (err: any) {
			parseError = err.message;
			headerStructs = [];
		} finally {
			loading = false;
		}
	}

	function selectPredefined(ps: PredefinedStruct) {
		predefinedStructId = ps.id;
		structText = ps.structText;
		structName = '';
	}

	$effect(() => {
		if (mode === 'predefined') loadPredefined();
	});

	// ── Predefined CRUD functions ──
	function startCreate() {
		pdMode = 'create';
		pdForm = { name: '', category: '', kind: 'general', description: '', structText: '' };
		pdEditId = null;
		pdError = '';
		pdInputMode = 'paste';
		pdHeaderStructs = [];
		pdHeaderError = '';
	}

	function startEdit(ps: PredefinedStruct) {
		pdMode = 'edit';
		pdForm = {
			name: ps.name,
			category: ps.category || '',
			kind: (ps.kind as 'metadata' | 'dlm' | 'general') || 'general',
			description: ps.description || '',
			structText: ps.structText
		};
		pdEditId = ps.id;
		pdError = '';
		pdInputMode = 'paste';
		pdHeaderStructs = [];
		pdHeaderError = '';
	}

	function cancelForm() {
		pdMode = 'list';
		pdError = '';
	}

	async function handlePdHeaderFile(e: Event) {
		const input = e.target as HTMLInputElement;
		const file = input.files?.[0];
		if (!file) return;
		pdHeaderParsing = true;
		pdHeaderError = '';
		try {
			const text = await file.text();
			pdForm.structText = text;
			if (!pdForm.name) {
				pdForm.name = file.name.replace(/\.[^.]+$/, '');
			}
			pdHeaderStructs = await parseHeader(file);
		} catch (err: any) {
			pdHeaderError = err.message;
		} finally {
			pdHeaderParsing = false;
		}
	}

	async function savePredefined() {
		if (!pdForm.name.trim() || !pdForm.structText.trim()) {
			pdError = 'Name and struct text are required';
			return;
		}
		pdSaving = true;
		pdError = '';
		try {
			if (pdMode === 'edit' && pdEditId != null) {
				await updateStruct(pdEditId, pdForm);
			} else {
				await createStruct(pdForm);
			}
			pdMode = 'list';
			await loadPredefined();
		} catch (err: any) {
			pdError = err.message || 'Save failed';
		} finally {
			pdSaving = false;
		}
	}

	async function deletePredefinedItem(id: number) {
		try {
			await deleteStruct(id);
			if (predefinedStructId === id) {
				predefinedStructId = null;
			}
			pdDeleteConfirm = null;
			await loadPredefined();
		} catch (err: any) {
			console.error('Delete failed', err);
		}
	}

	// ── Syntax highlighting ──
	const KEYWORDS = new Set([
		'struct', 'union', 'enum', 'typedef', 'const', 'signed', 'unsigned',
		'volatile', 'static', 'extern', 'inline', 'class', 'namespace'
	]);

	const TYPES = new Set([
		'void', 'char', 'short', 'int', 'long', 'float', 'double', 'bool',
		'uint8_t', 'uint16_t', 'uint32_t', 'uint64_t',
		'int8_t', 'int16_t', 'int32_t', 'int64_t',
		'size_t', 'ssize_t', 'ptrdiff_t', 'intptr_t', 'uintptr_t',
		'BYTE', 'WORD', 'DWORD', 'QWORD',
		'UINT8', 'UINT16', 'UINT32', 'UINT64',
		'INT8', 'INT16', 'INT32', 'INT64',
		'BOOL', 'BOOL8', 'BOOL32', 'BOOLEAN', 'CHAR', 'UCHAR', 'USHORT', 'ULONG',
		'u8', 'u16', 'u32', 'u64', 's8', 's16', 's32', 's64',
		'__le16', '__le32', '__le64', '__be16', '__be32', '__be64'
	]);

	function highlightCode(code: string): string {
		const lines = code.split('\n');
		return lines.map((line) => highlightLine(line)).join('\n');
	}

	function highlightLine(line: string): string {
		if (/^\s*#/.test(line)) {
			return `<span class="hl-preproc">${esc(line)}</span>`;
		}

		let result = '';
		let i = 0;
		while (i < line.length) {
			if (line[i] === '/' && line[i + 1] === '/') {
				result += `<span class="hl-comment">${esc(line.slice(i))}</span>`;
				break;
			}
			if (line[i] === '/' && line[i + 1] === '*') {
				const end = line.indexOf('*/', i + 2);
				if (end !== -1) {
					result += `<span class="hl-comment">${esc(line.slice(i, end + 2))}</span>`;
					i = end + 2;
					continue;
				} else {
					result += `<span class="hl-comment">${esc(line.slice(i))}</span>`;
					break;
				}
			}
			if (line[i] === '"') {
				let j = i + 1;
				while (j < line.length && line[j] !== '"') {
					if (line[j] === '\\') j++;
					j++;
				}
				j = Math.min(j + 1, line.length);
				result += `<span class="hl-string">${esc(line.slice(i, j))}</span>`;
				i = j;
				continue;
			}
			if (line[i] === "'") {
				let j = i + 1;
				while (j < line.length && line[j] !== "'") {
					if (line[j] === '\\') j++;
					j++;
				}
				j = Math.min(j + 1, line.length);
				result += `<span class="hl-string">${esc(line.slice(i, j))}</span>`;
				i = j;
				continue;
			}
			if (/[0-9]/.test(line[i]) || (line[i] === '-' && i + 1 < line.length && /[0-9]/.test(line[i + 1]))) {
				let j = i;
				if (line[j] === '-') j++;
				if (line[j] === '0' && (line[j + 1] === 'x' || line[j + 1] === 'X')) {
					j += 2;
					while (j < line.length && /[0-9a-fA-F]/.test(line[j])) j++;
				} else {
					while (j < line.length && /[0-9]/.test(line[j])) j++;
				}
				while (j < line.length && /[UuLl]/.test(line[j])) j++;
				result += `<span class="hl-number">${esc(line.slice(i, j))}</span>`;
				i = j;
				continue;
			}
			if (/[a-zA-Z_]/.test(line[i])) {
				let j = i;
				while (j < line.length && /[a-zA-Z0-9_]/.test(line[j])) j++;
				const word = line.slice(i, j);
				if (KEYWORDS.has(word)) {
					result += `<span class="hl-keyword">${esc(word)}</span>`;
				} else if (TYPES.has(word)) {
					result += `<span class="hl-type">${esc(word)}</span>`;
				} else if (word === '__attribute__' || word === 'packed') {
					result += `<span class="hl-keyword">${esc(word)}</span>`;
				} else {
					result += esc(word);
				}
				i = j;
				continue;
			}
			if (/[{}()\[\];:,*=|~<>]/.test(line[i])) {
				result += `<span class="hl-punct">${esc(line[i])}</span>`;
				i++;
				continue;
			}
			result += esc(line[i]);
			i++;
		}
		return result;
	}

	function esc(s: string): string {
		return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
	}

	let highlighted = $derived(highlightCode(structText));
	let pdHighlighted = $derived(highlightCode(pdForm.structText));

	// Sync scroll
	let textareaEl: HTMLTextAreaElement | null = $state(null);
	let backdropEl: HTMLPreElement | null = $state(null);
	let pdTextareaEl: HTMLTextAreaElement | null = $state(null);
	let pdBackdropEl: HTMLPreElement | null = $state(null);

	function syncScroll() {
		if (textareaEl && backdropEl) {
			backdropEl.scrollTop = textareaEl.scrollTop;
			backdropEl.scrollLeft = textareaEl.scrollLeft;
		}
	}

	function syncPdScroll() {
		if (pdTextareaEl && pdBackdropEl) {
			pdBackdropEl.scrollTop = pdTextareaEl.scrollTop;
			pdBackdropEl.scrollLeft = pdTextareaEl.scrollLeft;
		}
	}

	function handleKeydown(e: KeyboardEvent) {
		if (e.key === 'Tab' && textareaEl) {
			e.preventDefault();
			const start = textareaEl.selectionStart;
			const end = textareaEl.selectionEnd;
			structText = structText.substring(0, start) + '    ' + structText.substring(end);
			requestAnimationFrame(() => {
				if (textareaEl) {
					textareaEl.selectionStart = textareaEl.selectionEnd = start + 4;
				}
			});
		}
	}

	function handlePdKeydown(e: KeyboardEvent) {
		if (e.key === 'Tab' && pdTextareaEl) {
			e.preventDefault();
			const start = pdTextareaEl.selectionStart;
			const end = pdTextareaEl.selectionEnd;
			pdForm.structText = pdForm.structText.substring(0, start) + '    ' + pdForm.structText.substring(end);
			requestAnimationFrame(() => {
				if (pdTextareaEl) {
					pdTextareaEl.selectionStart = pdTextareaEl.selectionEnd = start + 4;
				}
			});
		}
	}
</script>

<div class="space-y-2">
	<div class="flex gap-2 text-xs">
		<button
			class="px-2 py-1 rounded {mode === 'text' ? 'bg-primary text-primary-foreground' : 'bg-muted'}"
			onclick={() => (mode = 'text')}
		>
			Text Input
		</button>
		<button
			class="px-2 py-1 rounded {mode === 'file' ? 'bg-primary text-primary-foreground' : 'bg-muted'}"
			onclick={() => (mode = 'file')}
		>
			Header File
		</button>
		<button
			class="px-2 py-1 rounded {mode === 'predefined' ? 'bg-primary text-primary-foreground' : 'bg-muted'}"
			onclick={() => (mode = 'predefined')}
		>
			Predefined
		</button>
	</div>

	{#if mode === 'text'}
		<div class="code-editor-wrap">
			<pre
				bind:this={backdropEl}
				class="code-backdrop"
				aria-hidden="true"
			>{@html highlighted + '\n'}</pre>
			<textarea
				bind:this={textareaEl}
				class="code-textarea"
				rows="12"
				spellcheck="false"
				autocomplete="off"
				autocorrect="off"
				autocapitalize="off"
				placeholder={'struct MyStruct {\n    uint32_t magic;\n    uint16_t version;\n    uint8_t flags;\n};'}
				bind:value={structText}
				oninput={syncScroll}
				onscroll={syncScroll}
				onkeydown={handleKeydown}
			></textarea>
		</div>
	{:else if mode === 'file'}
		<div class="space-y-2">
			<input
				type="file"
				class="h-7 w-full text-xs file:mr-2 file:rounded file:border-0 file:bg-muted file:px-2 file:text-xs"
				accept=".h,.hpp,.hh"
				onchange={handleHeaderFile}
			/>
			{#if loading}
				<div class="text-xs text-muted-foreground">Parsing header...</div>
			{/if}
			{#if parseError}
				<div class="text-xs text-destructive">{parseError}</div>
			{/if}
			{#if headerStructs.length > 1}
				<select class="h-7 w-full rounded-md border border-input bg-background px-2 text-xs" bind:value={structName}>
					{#each headerStructs as s}
						<option value={s.name}>{s.name} ({s.fields.length} fields)</option>
					{/each}
				</select>
			{:else if headerStructs.length === 1}
				<div class="text-xs text-green-600">Found: {headerStructs[0].name} ({headerStructs[0].fields.length} fields)</div>
			{/if}
		</div>
	{:else}
		<!-- Predefined mode -->
		{#if pdMode === 'list'}
			<div class="space-y-1">
				<div class="flex items-center justify-between mb-1">
					<span class="text-[11px] text-muted-foreground">{predefinedList.length} struct{predefinedList.length !== 1 ? 's' : ''}</span>
					<button
						class="px-2 py-0.5 text-[11px] rounded bg-primary text-primary-foreground hover:bg-primary/90 transition-colors"
						onclick={startCreate}
					>
						+ New
					</button>
				</div>
				<div class="max-h-48 overflow-y-auto space-y-0.5">
					{#if predefinedList.length === 0}
						<div class="text-xs text-muted-foreground p-2 text-center">No predefined structs yet</div>
					{/if}
					{#each predefinedList as ps}
						<div
							class="flex items-center gap-1 rounded text-xs transition-colors
								{predefinedStructId === ps.id ? 'bg-primary/10 border border-primary' : 'hover:bg-muted'}"
						>
							<button
								class="flex-1 text-left px-2 py-1.5 min-w-0"
								onclick={() => selectPredefined(ps)}
							>
								<div class="font-medium truncate">{ps.name}</div>
								<div class="flex items-center gap-1 mt-0.5">
									{#if ps.kind && ps.kind !== 'general'}
										<span
											class="px-1.5 py-0.5 rounded text-[10px] {ps.kind === 'metadata'
												? 'bg-blue-500/15 text-blue-600'
												: 'bg-amber-500/15 text-amber-700'}"
										>
											{ps.kind}
										</span>
									{/if}
									{#if ps.category}
										<span class="px-1.5 py-0.5 rounded text-[10px] bg-muted text-muted-foreground">{ps.category}</span>
									{/if}
									{#if ps.description}
										<span class="text-muted-foreground truncate">{ps.description}</span>
									{/if}
								</div>
							</button>
							<div class="flex items-center gap-0.5 pr-1 shrink-0">
								<button
									class="p-1 rounded hover:bg-muted text-muted-foreground hover:text-foreground transition-colors"
									title="Edit"
									onclick={() => startEdit(ps)}
								>
									<svg class="size-3" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/><path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/></svg>
								</button>
								{#if pdDeleteConfirm === ps.id}
									<button
										class="px-1.5 py-0.5 rounded text-[10px] bg-destructive text-destructive-foreground"
										onclick={() => deletePredefinedItem(ps.id)}
									>
										Confirm
									</button>
									<button
										class="px-1 py-0.5 rounded text-[10px] hover:bg-muted"
										onclick={() => (pdDeleteConfirm = null)}
									>
										Cancel
									</button>
								{:else}
									<button
										class="p-1 rounded hover:bg-destructive/10 text-muted-foreground hover:text-destructive transition-colors"
										title="Delete"
										onclick={() => (pdDeleteConfirm = ps.id)}
									>
										<svg class="size-3" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M3 6h18"/><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/></svg>
									</button>
								{/if}
							</div>
						</div>
					{/each}
				</div>
			</div>
		{:else}
			<!-- Create / Edit form -->
			<div class="space-y-2">
				<div class="flex items-center justify-between">
					<span class="text-xs font-medium">{pdMode === 'create' ? 'New Predefined Struct' : 'Edit Struct'}</span>
					<button class="text-[11px] text-muted-foreground hover:text-foreground" onclick={cancelForm}>Cancel</button>
				</div>

				<div class="grid grid-cols-2 gap-2">
					<input
						class="h-7 w-full rounded-md border border-input bg-background px-2 text-xs"
						placeholder="Name *"
						bind:value={pdForm.name}
					/>
					<input
						class="h-7 w-full rounded-md border border-input bg-background px-2 text-xs"
						placeholder="Category"
						bind:value={pdForm.category}
					/>
				</div>
				<div class="grid grid-cols-2 gap-2">
					<select
						class="h-7 w-full rounded-md border border-input bg-background px-2 text-xs"
						bind:value={pdForm.kind}
						title="struct 용도 구분"
					>
						<option value="general">general</option>
						<option value="metadata">metadata</option>
						<option value="dlm">dlm</option>
					</select>
					<input
						class="h-7 w-full rounded-md border border-input bg-background px-2 text-xs"
						placeholder="Description"
						bind:value={pdForm.description}
					/>
				</div>

				<!-- Struct text input mode toggle -->
				<div class="flex gap-1 text-[11px]">
					<button
						class="px-2 py-0.5 rounded {pdInputMode === 'paste' ? 'bg-muted font-medium' : 'hover:bg-muted'}"
						onclick={() => (pdInputMode = 'paste')}
					>
						Paste / Type
					</button>
					<button
						class="px-2 py-0.5 rounded {pdInputMode === 'file' ? 'bg-muted font-medium' : 'hover:bg-muted'}"
						onclick={() => (pdInputMode = 'file')}
					>
						Header File
					</button>
				</div>

				{#if pdInputMode === 'file'}
					<input
						type="file"
						class="h-6 w-full text-xs file:mr-2 file:rounded file:border-0 file:bg-muted file:px-2 file:text-xs"
						accept=".h,.hpp,.hh"
						onchange={handlePdHeaderFile}
					/>
					{#if pdHeaderParsing}
						<div class="text-[11px] text-muted-foreground">Parsing...</div>
					{/if}
					{#if pdHeaderError}
						<div class="text-[11px] text-destructive">{pdHeaderError}</div>
					{/if}
					{#if pdHeaderStructs.length > 0}
						<div class="text-[11px] text-green-600">
							Found {pdHeaderStructs.length} struct{pdHeaderStructs.length > 1 ? 's' : ''}: {pdHeaderStructs.map(s => s.name).join(', ')}
						</div>
					{/if}
				{/if}

				<!-- Struct text editor with syntax highlighting -->
				<div class="code-editor-wrap" style="max-height: 200px;">
					<pre
						bind:this={pdBackdropEl}
						class="code-backdrop"
						aria-hidden="true"
					>{@html pdHighlighted + '\n'}</pre>
					<textarea
						bind:this={pdTextareaEl}
						class="code-textarea"
						rows="8"
						spellcheck="false"
						autocomplete="off"
						autocorrect="off"
						autocapitalize="off"
						placeholder={'struct MyStruct { ... };'}
						bind:value={pdForm.structText}
						oninput={syncPdScroll}
						onscroll={syncPdScroll}
						onkeydown={handlePdKeydown}
						style="min-height: 120px;"
					></textarea>
				</div>

				{#if pdError}
					<div class="text-[11px] text-destructive">{pdError}</div>
				{/if}

				<div class="flex justify-end gap-1">
					<button
						class="px-3 py-1 text-[11px] rounded border hover:bg-muted transition-colors"
						onclick={cancelForm}
					>
						Cancel
					</button>
					<button
						class="px-3 py-1 text-[11px] rounded bg-primary text-primary-foreground hover:bg-primary/90 transition-colors disabled:opacity-50"
						onclick={savePredefined}
						disabled={pdSaving || !pdForm.name.trim() || !pdForm.structText.trim()}
					>
						{pdSaving ? 'Saving...' : pdMode === 'create' ? 'Create' : 'Update'}
					</button>
				</div>
			</div>
		{/if}
	{/if}
</div>

<style>
	.code-editor-wrap {
		position: relative;
		border: 1px solid hsl(var(--border));
		border-radius: 0.5rem;
		overflow: hidden;
		background: hsl(var(--background));
	}

	.code-backdrop,
	.code-textarea {
		font-family: ui-monospace, 'Cascadia Code', 'Fira Code', monospace;
		font-size: 12px;
		line-height: 1.5;
		padding: 12px;
		margin: 0;
		white-space: pre;
		word-wrap: normal;
		overflow: auto;
		tab-size: 4;
	}

	.code-backdrop {
		position: absolute;
		inset: 0;
		pointer-events: none;
		color: hsl(var(--foreground));
		overflow: hidden;
	}

	.code-textarea {
		position: relative;
		width: 100%;
		min-height: 200px;
		resize: vertical;
		border: none;
		outline: none;
		background: transparent;
		color: transparent;
		caret-color: hsl(var(--foreground));
		z-index: 1;
	}

	.code-textarea::placeholder {
		color: hsl(var(--muted-foreground) / 0.5);
	}

	.code-textarea::selection {
		background: hsl(var(--primary) / 0.25);
		color: transparent;
	}

	/* ── Syntax highlight tokens ── */
	:global(.hl-keyword) {
		color: oklch(0.65 0.25 280);  /* purple */
		font-weight: 600;
	}
	:global(.hl-type) {
		color: oklch(0.65 0.18 200);  /* cyan-blue */
	}
	:global(.hl-number) {
		color: oklch(0.7 0.18 80);    /* orange-yellow */
	}
	:global(.hl-string) {
		color: oklch(0.65 0.18 140);  /* green */
	}
	:global(.hl-comment) {
		color: hsl(var(--muted-foreground) / 0.7);
		font-style: italic;
	}
	:global(.hl-preproc) {
		color: oklch(0.6 0.15 30);    /* reddish */
	}
	:global(.hl-punct) {
		color: hsl(var(--muted-foreground));
	}
</style>
