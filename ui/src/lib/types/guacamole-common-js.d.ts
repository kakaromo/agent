declare module 'guacamole-common-js' {
	export class Client {
		constructor(tunnel: Tunnel);
		connect(data?: string): void;
		disconnect(): void;
		getDisplay(): Display;
		sendKeyEvent(pressed: number, keysym: number): void;
		sendMouseState(state: Mouse.State): void;
		sendSize(width: number, height: number): void;
		createClipboardStream(mimetype: string): OutputStream;
		onstatechange: ((state: number) => void) | null;
		onerror: ((status: Status) => void) | null;
		onclipboard: ((stream: InputStream, mimetype: string) => void) | null;
	}

	export class InputStream {
		sendAck(message: string, code: number): void;
	}

	export class OutputStream {}

	export class StringReader {
		constructor(stream: InputStream);
		ontext: ((text: string) => void) | null;
		onend: (() => void) | null;
	}

	export class StringWriter {
		constructor(stream: OutputStream);
		sendText(text: string): void;
		sendEnd(): void;
	}

	export class Tunnel {
		onerror: ((status: Status) => void) | null;
		onstatechange: ((state: number) => void) | null;
	}

	export class WebSocketTunnel extends Tunnel {
		constructor(url: string);
	}

	export class HTTPTunnel extends Tunnel {
		constructor(url: string, crossDomain?: boolean, extraHeaders?: object);
	}

	export class Display {
		getElement(): HTMLElement;
		getWidth(): number;
		getHeight(): number;
		getScale(): number;
		scale(scale: number): void;
		onresize: ((width: number, height: number) => void) | null;
	}

	export class Keyboard {
		constructor(element: Document | HTMLElement);
		onkeydown: ((keysym: number) => void) | null;
		onkeyup: ((keysym: number) => void) | null;
	}

	export class Mouse {
		constructor(element: HTMLElement);
		onmousedown: ((state: Mouse.State) => void) | null;
		onmouseup: ((state: Mouse.State) => void) | null;
		onmousemove: ((state: Mouse.State) => void) | null;
	}

	export namespace Mouse {
		export class State {
			constructor(
				x: number,
				y: number,
				left: boolean,
				middle: boolean,
				right: boolean,
				up: boolean,
				down: boolean
			);
			x: number;
			y: number;
			left: boolean;
			middle: boolean;
			right: boolean;
			up: boolean;
			down: boolean;
		}

		export class Touchpad {
			constructor(element: HTMLElement);
			onmousedown: ((state: State) => void) | null;
			onmouseup: ((state: State) => void) | null;
			onmousemove: ((state: State) => void) | null;
		}

		export class Touchscreen {
			constructor(element: HTMLElement);
			onmousedown: ((state: State) => void) | null;
			onmouseup: ((state: State) => void) | null;
			onmousemove: ((state: State) => void) | null;
		}
	}

	export class Status {
		code: number;
		message: string;
		isError(): boolean;
	}

	export namespace Status {
		export enum Code {
			SUCCESS = 0x0000,
			UNSUPPORTED = 0x0100
		}
	}

	export default {
		Client,
		Tunnel,
		WebSocketTunnel,
		HTTPTunnel,
		Display,
		Keyboard,
		Mouse,
		Status,
		InputStream,
		OutputStream,
		StringReader,
		StringWriter
	};
}
