<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import { Editor } from '@tiptap/core';
	import StarterKit from '@tiptap/starter-kit';
	import Link from '@tiptap/extension-link';
	import Image from '@tiptap/extension-image';
	import { Table, TableRow, TableCell, TableHeader } from '@tiptap/extension-table';
	import Underline from '@tiptap/extension-underline';
	import Placeholder from '@tiptap/extension-placeholder';
	import TextAlign from '@tiptap/extension-text-align';

	// Lucide icons
	import BoldIcon from '@lucide/svelte/icons/bold';
	import ItalicIcon from '@lucide/svelte/icons/italic';
	import UnderlineIcon from '@lucide/svelte/icons/underline';
	import StrikethroughIcon from '@lucide/svelte/icons/strikethrough';
	import Heading1Icon from '@lucide/svelte/icons/heading-1';
	import Heading2Icon from '@lucide/svelte/icons/heading-2';
	import Heading3Icon from '@lucide/svelte/icons/heading-3';
	import ListIcon from '@lucide/svelte/icons/list';
	import ListOrderedIcon from '@lucide/svelte/icons/list-ordered';
	import QuoteIcon from '@lucide/svelte/icons/quote';
	import CodeIcon from '@lucide/svelte/icons/code';
	import LinkIcon from '@lucide/svelte/icons/link';
	import ImageIcon from '@lucide/svelte/icons/image-plus';
	import TableIcon from '@lucide/svelte/icons/table';
	import RowsIcon from '@lucide/svelte/icons/between-horizontal-start';
	import ColsIcon from '@lucide/svelte/icons/between-vertical-start';
	import TrashIcon from '@lucide/svelte/icons/trash-2';
	import AlignLeftIcon from '@lucide/svelte/icons/align-left';
	import AlignCenterIcon from '@lucide/svelte/icons/align-center';
	import AlignRightIcon from '@lucide/svelte/icons/align-right';
	import UndoIcon from '@lucide/svelte/icons/undo-2';
	import RedoIcon from '@lucide/svelte/icons/redo-2';
	import MinusIcon from '@lucide/svelte/icons/minus';

	interface Props {
		content?: string;
		placeholder?: string;
		editable?: boolean;
		onUpdate?: (html: string) => void;
	}

	let { content = '', placeholder = 'Write something...', editable = true, onUpdate }: Props = $props();

	let element: HTMLDivElement;
	let editor: Editor | undefined = $state(undefined);

	onMount(() => {
		editor = new Editor({
			element,
			extensions: [
				StarterKit.configure({
					heading: { levels: [1, 2, 3] }
				}),
				Link.configure({ openOnClick: false }),
				Image.configure({
					allowBase64: true,
					resize: {
						enabled: true,
						minWidth: 50,
						minHeight: 50,
						alwaysPreserveAspectRatio: true
					}
				}),
				Table.configure({ resizable: true }),
				TableRow,
				TableCell,
				TableHeader,
				Underline,
				Placeholder.configure({ placeholder }),
				TextAlign.configure({ types: ['heading', 'paragraph'] })
			],
			content,
			editable,
			onTransaction: () => {
				// Force Svelte reactivity
				editor = editor;
				inTable = editor?.isActive('table') ?? false;
			},
			onUpdate: ({ editor: e }) => {
				onUpdate?.(e.getHTML());
			}
		});
	});

	onDestroy(() => {
		editor?.destroy();
	});

	export function getHTML(): string {
		return editor?.getHTML() ?? '';
	}

	export function setContent(html: string) {
		editor?.commands.setContent(html);
	}

	export function isEmpty(): boolean {
		return editor?.isEmpty ?? true;
	}

	function addLink() {
		const prev = editor?.getAttributes('link').href ?? '';
		const url = window.prompt('URL', prev);
		if (url === null) return;
		if (url === '') {
			editor?.chain().focus().extendMarkRange('link').unsetLink().run();
		} else {
			editor?.chain().focus().extendMarkRange('link').setLink({ href: url }).run();
		}
	}

	function addImage() {
		const url = window.prompt('Image URL');
		if (url) {
			editor?.chain().focus().setImage({ src: url }).run();
		}
	}

	function insertTable() {
		const input = window.prompt('Table size (rows x cols)', '3x3');
		if (!input) return;
		const match = input.match(/(\d+)\s*[x×,]\s*(\d+)/i);
		const rows = match ? Math.max(1, parseInt(match[1])) : 3;
		const cols = match ? Math.max(1, parseInt(match[2])) : 3;
		editor?.chain().focus().insertTable({ rows, cols, withHeaderRow: true }).run();
	}

	let inTable = $state(false);

	const btnClass = 'p-1.5 rounded hover:bg-accent transition-colors disabled:opacity-30';
	const activeClass = 'bg-accent text-accent-foreground';
	const separatorClass = 'w-px h-5 bg-border mx-0.5';
</script>

<div class="border rounded-md overflow-hidden bg-background">
	{#if editor && editable}
		<div class="flex flex-wrap items-center gap-0.5 px-2 py-1.5 border-b bg-muted/30">
			<!-- Text formatting -->
			<button type="button" class="{btnClass} {editor.isActive('bold') ? activeClass : ''}" onclick={() => editor?.chain().focus().toggleBold().run()} title="Bold">
				<BoldIcon class="size-4" />
			</button>
			<button type="button" class="{btnClass} {editor.isActive('italic') ? activeClass : ''}" onclick={() => editor?.chain().focus().toggleItalic().run()} title="Italic">
				<ItalicIcon class="size-4" />
			</button>
			<button type="button" class="{btnClass} {editor.isActive('underline') ? activeClass : ''}" onclick={() => editor?.chain().focus().toggleUnderline().run()} title="Underline">
				<UnderlineIcon class="size-4" />
			</button>
			<button type="button" class="{btnClass} {editor.isActive('strike') ? activeClass : ''}" onclick={() => editor?.chain().focus().toggleStrike().run()} title="Strikethrough">
				<StrikethroughIcon class="size-4" />
			</button>

			<div class={separatorClass}></div>

			<!-- Headings & structure -->
			<button type="button" class="{btnClass} {editor.isActive('heading', { level: 1 }) ? activeClass : ''}" onclick={() => editor?.chain().focus().toggleHeading({ level: 1 }).run()} title="Heading 1">
				<Heading1Icon class="size-4" />
			</button>
			<button type="button" class="{btnClass} {editor.isActive('heading', { level: 2 }) ? activeClass : ''}" onclick={() => editor?.chain().focus().toggleHeading({ level: 2 }).run()} title="Heading 2">
				<Heading2Icon class="size-4" />
			</button>
			<button type="button" class="{btnClass} {editor.isActive('heading', { level: 3 }) ? activeClass : ''}" onclick={() => editor?.chain().focus().toggleHeading({ level: 3 }).run()} title="Heading 3">
				<Heading3Icon class="size-4" />
			</button>
			<button type="button" class="{btnClass} {editor.isActive('bulletList') ? activeClass : ''}" onclick={() => editor?.chain().focus().toggleBulletList().run()} title="Bullet List">
				<ListIcon class="size-4" />
			</button>
			<button type="button" class="{btnClass} {editor.isActive('orderedList') ? activeClass : ''}" onclick={() => editor?.chain().focus().toggleOrderedList().run()} title="Ordered List">
				<ListOrderedIcon class="size-4" />
			</button>
			<button type="button" class="{btnClass} {editor.isActive('blockquote') ? activeClass : ''}" onclick={() => editor?.chain().focus().toggleBlockquote().run()} title="Blockquote">
				<QuoteIcon class="size-4" />
			</button>
			<button type="button" class="{btnClass} {editor.isActive('codeBlock') ? activeClass : ''}" onclick={() => editor?.chain().focus().toggleCodeBlock().run()} title="Code Block">
				<CodeIcon class="size-4" />
			</button>
			<button type="button" class={btnClass} onclick={() => editor?.chain().focus().setHorizontalRule().run()} title="Horizontal Rule">
				<MinusIcon class="size-4" />
			</button>

			<div class={separatorClass}></div>

			<!-- Insert -->
			<button type="button" class="{btnClass} {editor.isActive('link') ? activeClass : ''}" onclick={addLink} title="Link">
				<LinkIcon class="size-4" />
			</button>
			<button type="button" class={btnClass} onclick={addImage} title="Image">
				<ImageIcon class="size-4" />
			</button>
			<button type="button" class={btnClass} onclick={insertTable} title="Insert Table">
				<TableIcon class="size-4" />
			</button>
			{#if inTable}
				<button type="button" class={btnClass} onclick={() => editor?.chain().focus().addRowAfter().run()} title="Add Row">
					<RowsIcon class="size-4" />
				</button>
				<button type="button" class={btnClass} onclick={() => editor?.chain().focus().addColumnAfter().run()} title="Add Column">
					<ColsIcon class="size-4" />
				</button>
				<button type="button" class={btnClass} onclick={() => editor?.chain().focus().deleteRow().run()} title="Delete Row">
					<span class="relative"><RowsIcon class="size-4 opacity-50" /><TrashIcon class="size-2.5 absolute -bottom-0.5 -right-0.5 text-destructive" /></span>
				</button>
				<button type="button" class={btnClass} onclick={() => editor?.chain().focus().deleteColumn().run()} title="Delete Column">
					<span class="relative"><ColsIcon class="size-4 opacity-50" /><TrashIcon class="size-2.5 absolute -bottom-0.5 -right-0.5 text-destructive" /></span>
				</button>
				<button type="button" class="{btnClass} text-destructive" onclick={() => editor?.chain().focus().deleteTable().run()} title="Delete Table">
					<TrashIcon class="size-4" />
				</button>
			{/if}

			<div class={separatorClass}></div>

			<!-- Alignment -->
			<button type="button" class="{btnClass} {editor.isActive({ textAlign: 'left' }) ? activeClass : ''}" onclick={() => editor?.chain().focus().setTextAlign('left').run()} title="Align Left">
				<AlignLeftIcon class="size-4" />
			</button>
			<button type="button" class="{btnClass} {editor.isActive({ textAlign: 'center' }) ? activeClass : ''}" onclick={() => editor?.chain().focus().setTextAlign('center').run()} title="Align Center">
				<AlignCenterIcon class="size-4" />
			</button>
			<button type="button" class="{btnClass} {editor.isActive({ textAlign: 'right' }) ? activeClass : ''}" onclick={() => editor?.chain().focus().setTextAlign('right').run()} title="Align Right">
				<AlignRightIcon class="size-4" />
			</button>

			<div class={separatorClass}></div>

			<!-- Undo/Redo -->
			<button type="button" class={btnClass} disabled={!editor.can().undo()} onclick={() => editor?.chain().focus().undo().run()} title="Undo">
				<UndoIcon class="size-4" />
			</button>
			<button type="button" class={btnClass} disabled={!editor.can().redo()} onclick={() => editor?.chain().focus().redo().run()} title="Redo">
				<RedoIcon class="size-4" />
			</button>
		</div>
	{/if}

	<div bind:this={element} class="tiptap-wrapper"></div>
</div>
