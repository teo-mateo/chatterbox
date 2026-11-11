/**
 * The chatterbox processes: one long-lived `listen` child whose output is
 * turned into events, and short commands run to completion. No shell is
 * involved anywhere; arguments go as a vector and message text on stdin.
 */

import { type ChildProcess, spawn } from "node:child_process";
import { existsSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";

import { type ChatMessage, parseLine } from "./inbox.ts";

export function binary(): string {
	if (process.env.CHATTERBOX_BIN) return process.env.CHATTERBOX_BIN;
	const local = join(homedir(), ".local", "bin", "chatterbox");
	return existsSync(local) ? local : "chatterbox";
}

export const INSTALL_HINT =
	"The chatterbox binary was not found. Install it with: " +
	"curl -fsSL https://raw.githubusercontent.com/teo-mateo/chatterbox/main/scripts/install.sh | sh";

/** Strips the "chatterbox: " prefix of every line of a diagnostic. */
export function diagnostic(stderr: string): string {
	return stderr
		.split("\n")
		.map((l) => l.replace(/^chatterbox: ?/, "").trimEnd())
		.filter((l) => l.length > 0)
		.join("\n");
}

/** What the exit code of `listen` means, for an agent that has to decide what to do. */
export function listenExit(code: number | null): string {
	switch (code) {
		case 0:
			return "stopped";
		case 1:
			return "internal error";
		case 2:
			return "usage or configuration error";
		case 4:
			return "nickname already in use";
		case 5:
			return "server certificate not trusted";
		case 6:
			return "refused by the server or the channel";
		case 9:
			return "could not reach or register with the server";
		default:
			return code === null ? "killed" : `exit code ${code}`;
	}
}

export interface CommandResult {
	code: number;
	stdout: string;
	stderr: string;
	/** The binary could not be started at all. */
	missing: boolean;
}

/** Runs one chatterbox command to completion. `stdin` is the message text for `-`. */
export function run(args: string[], stdin?: string, signal?: AbortSignal): Promise<CommandResult> {
	return new Promise((resolve) => {
		let child: ChildProcess;
		try {
			child = spawn(binary(), args, { stdio: ["pipe", "pipe", "pipe"], signal });
		} catch (err) {
			resolve({ code: 127, stdout: "", stderr: String(err), missing: true });
			return;
		}
		let stdout = "";
		let stderr = "";
		let done = false;
		const finish = (result: CommandResult) => {
			if (done) return;
			done = true;
			resolve(result);
		};
		child.stdout?.on("data", (d) => (stdout += d));
		child.stderr?.on("data", (d) => (stderr += d));
		child.on("error", (err: NodeJS.ErrnoException) =>
			finish({ code: 127, stdout, stderr: stderr || err.message, missing: err.code === "ENOENT" }),
		);
		child.on("close", (code) => finish({ code: code ?? 1, stdout, stderr, missing: false }));
		child.stdin?.on("error", () => {});
		child.stdin?.end(stdin ?? "");
	});
}

export interface ListenerEvents {
	onMessage(message: ChatMessage): void;
	/** A `chatterbox:` line from stderr, without the prefix. */
	onDiagnostic(line: string): void;
	/** The listener ended after it had joined. */
	onExit(code: number | null, lastDiagnostic: string): void;
}

export interface Joined {
	nick: string;
	channel: string;
}

/** A running `chatterbox listen --json`. */
export class Listener {
	private child: ChildProcess | undefined;
	private stopping = false;
	nick = "";
	channel = "";
	pid: number | undefined;

	constructor(private readonly events: ListenerEvents) {}

	get running(): boolean {
		return this.child !== undefined;
	}

	/**
	 * Starts the listener and resolves once it has joined. It is a plain child
	 * of this process with pipes: chatterbox then notices on its own when this
	 * process goes away (parent watch and pipe hang-up).
	 */
	start(nick: string, channel: string | undefined, timeoutMs = 30000): Promise<Joined> {
		if (this.child) return Promise.reject(new Error("a listener is already running"));
		this.stopping = false;
		const args = ["listen", "--as", nick, "--json"];
		if (channel) args.push("--channel", channel);

		return new Promise((resolve, reject) => {
			let child: ChildProcess;
			try {
				child = spawn(binary(), args, { stdio: ["ignore", "pipe", "pipe"] });
			} catch (err) {
				reject(new Error(`${INSTALL_HINT} (${String(err)})`));
				return;
			}
			this.child = child;
			this.pid = child.pid;
			let joined = false;
			let diagnostics: string[] = [];
			let settled = false;

			const timer = setTimeout(() => {
				if (settled) return;
				settled = true;
				this.kill();
				reject(new Error(`the listener did not join within ${Math.round(timeoutMs / 1000)} s`));
			}, timeoutMs);

			lines(child.stdout, (line) => {
				const message = parseLine(line);
				if (message) this.events.onMessage(message);
			});
			lines(child.stderr, (raw) => {
				const line = raw.replace(/^chatterbox: ?/, "");
				if (line.trim() === "") return;
				diagnostics.push(line);
				if (diagnostics.length > 20) diagnostics = diagnostics.slice(-20);
				const m = /^joined (\S+) as (\S+)$/.exec(line) ?? /^now listening on (\S+)$/.exec(line);
				if (m) {
					this.channel = m[1];
					if (m[2]) this.nick = m[2];
				}
				if (!joined && /^joined /.test(line)) {
					joined = true;
					settled = true;
					clearTimeout(timer);
					diagnostics = [];
					resolve({ nick: this.nick, channel: this.channel });
					return;
				}
				if (joined) this.events.onDiagnostic(line);
			});

			child.on("error", (err: NodeJS.ErrnoException) => {
				clearTimeout(timer);
				this.child = undefined;
				if (settled) return;
				settled = true;
				reject(new Error(err.code === "ENOENT" ? INSTALL_HINT : err.message));
			});
			child.on("close", (code) => {
				clearTimeout(timer);
				this.child = undefined;
				const last = diagnostics.join("\n");
				if (!settled) {
					settled = true;
					reject(new Error(`${listenExit(code)}: ${last || "the listener exited without a diagnostic"}`));
					return;
				}
				if (joined && !this.stopping) this.events.onExit(code, last);
			});
		});
	}

	/** Asks the listener to leave (it sends QUIT) and waits for it to end. */
	stop(graceMs = 3000): Promise<void> {
		const child = this.child;
		if (!child) return Promise.resolve();
		this.stopping = true;
		return new Promise((resolve) => {
			const force = setTimeout(() => child.kill("SIGKILL"), graceMs);
			child.once("close", () => {
				clearTimeout(force);
				resolve();
			});
			child.kill("SIGTERM");
		});
	}

	/** Ends the listener without waiting: for the paths that cannot await. */
	kill(): void {
		this.stopping = true;
		this.child?.kill("SIGTERM");
	}
}

/** Calls `each` for every complete line of a stream. */
function lines(stream: NodeJS.ReadableStream | null, each: (line: string) => void): void {
	if (!stream) return;
	let buffer = "";
	stream.setEncoding("utf8");
	stream.on("data", (chunk: string) => {
		buffer += chunk;
		let nl = buffer.indexOf("\n");
		while (nl >= 0) {
			const line = buffer.slice(0, nl).replace(/\r$/, "");
			buffer = buffer.slice(nl + 1);
			try {
				each(line);
			} catch {
				// A handler must not take the stream down with it.
			}
			nl = buffer.indexOf("\n");
		}
	});
}
