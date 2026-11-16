import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
	getCoverColumnWidth,
	IOS_COVER_COLUMN_PROPERTY,
	IOS_STATUS_BAND_HEIGHT_PX,
	installIosCoverColumn,
} from "./forkIosCoverColumn";

const DEVICE_WIDTH_META =
	"width=device-width, initial-scale=1, maximum-scale=1, user-scalable=no";

// The folded iPhone Duo's cover display on iOS 27.2, measured on the
// simulator's web clip.
const DUO_COVER = {
	screenWidth: 466,
	layoutWidth: 382,
	layoutHeight: 678,
	reportedRightInset: 0,
};

describe("getCoverColumnWidth", () => {
	it("takes back the Duo cover display's status column", () => {
		expect(getCoverColumnWidth(DUO_COVER)).toBe(84);
	});

	it.each([
		["a layout that spans its screen", { screenWidth: 390, layoutWidth: 390 }],
		[
			"iOS 27.1, which reports the column as an inset",
			{ reportedRightInset: 84 },
		],
		["iPad multitasking", { screenWidth: 744, layoutWidth: 660 }],
		[
			"a gap wider than a status column",
			{ screenWidth: 500, layoutWidth: 320 },
		],
		["landscape", { layoutWidth: 400, layoutHeight: 380 }],
	])("leaves %s alone", (_, geometry) => {
		expect(getCoverColumnWidth({ ...DUO_COVER, ...geometry })).toBe(0);
	});
});

// jsdom has TouchEvent but no Touch.
class FakeTouch {
	readonly identifier: number;
	readonly target: EventTarget;
	readonly clientX: number;
	readonly clientY: number;
	readonly screenX = 0;
	readonly screenY = 0;
	readonly pageX: number;
	readonly pageY: number;
	readonly radiusX = 0;
	readonly radiusY = 0;
	readonly rotationAngle = 0;
	readonly force = 1;

	constructor(init: TouchInit) {
		this.identifier = init.identifier;
		this.target = init.target;
		this.clientX = init.clientX ?? 0;
		this.clientY = init.clientY ?? 0;
		this.pageX = this.clientX;
		this.pageY = this.clientY;
	}
}

// Vitest's jsdom window is not the one jsdom checks an event's `view`
// against, so the module's `view: window` fails to construct.
class ViewlessPointerEvent extends PointerEvent {
	constructor(type: string, init?: PointerEventInit) {
		super(type, { ...init, view: null });
	}
}

class ViewlessTouchEvent extends TouchEvent {
	constructor(type: string, init?: TouchEventInit) {
		super(type, { ...init, view: null });
	}
}

class ViewlessMouseEvent extends MouseEvent {
	constructor(type: string, init?: MouseEventInit) {
		super(type, { ...init, view: null });
	}
}

// The Duo's displays as iOS 27.2 lays a web clip out on them at device width.
// iOS reports the cover display's screen width on both, and innerWidth keeps
// the visible width, which leaves the column out.
const COVER = { width: 382, height: 678 };
const INNER = { width: 867, height: 669 };

type Display = typeof COVER;

/**
 * Stands in for iOS: lays the page out at device width or at the viewport
 * meta's size, and reports the page as a home-screen web app unless told
 * otherwise. Returns a function that switches the display.
 */
function emulateDuo(
	meta: HTMLMetaElement,
	{ standalone = true } = {},
): (display: Display) => void {
	let current = COVER;
	const size = () => {
		const width = /\bwidth=(\d+)/.exec(meta.content);
		const height = /\bheight=(\d+)/.exec(meta.content);
		return {
			width: width ? Number(width[1]) : current.width,
			height: height ? Number(height[1]) : current.height,
		};
	};
	const root = document.documentElement;
	vi.spyOn(root, "clientWidth", "get").mockImplementation(() => size().width);
	vi.spyOn(root, "clientHeight", "get").mockImplementation(() => size().height);
	vi.spyOn(window.screen, "width", "get").mockReturnValue(
		DUO_COVER.screenWidth,
	);
	vi.stubGlobal("matchMedia", (query: string) => ({
		matches:
			(query === "(display-mode: standalone)" && standalone) ||
			(query === "(orientation: portrait)" && current.height > current.width),
	}));
	const setDisplay = (display: Display) => {
		current = display;
		vi.stubGlobal("innerWidth", display.width);
	};
	setDisplay(COVER);
	return setDisplay;
}

const frames = async (count: number) => {
	for (let i = 0; i < count; i++) {
		await new Promise((resolve) => window.requestAnimationFrame(resolve));
	}
};

/** A vertical scroller with 500px of room, around `child`. */
function wrapInScroller(child: Element, scrollTop: number): HTMLDivElement {
	const scroller = document.createElement("div");
	scroller.style.overflowY = "auto";
	vi.spyOn(scroller, "scrollHeight", "get").mockReturnValue(1000);
	vi.spyOn(scroller, "clientHeight", "get").mockReturnValue(500);
	scroller.appendChild(child);
	document.body.appendChild(scroller);
	scroller.scrollTop = scrollTop;
	return scroller;
}

function pointer(type: string, x: number, y: number): PointerEvent {
	return new window.PointerEvent(type, {
		bubbles: true,
		cancelable: true,
		pointerId: 7,
		pointerType: "touch",
		isPrimary: true,
		clientX: x,
		clientY: y,
	});
}

function touchAt(
	type: string,
	point: { x: number; y: number; time?: number },
	target: EventTarget = document.documentElement,
): TouchEvent {
	const touch = new window.Touch({
		identifier: 7,
		target,
		clientX: point.x,
		clientY: point.y,
	});
	const active = type === "touchend" ? [] : [touch];
	const event = new window.TouchEvent(type, {
		bubbles: true,
		cancelable: true,
		touches: active,
		targetTouches: active,
		changedTouches: [touch],
	});
	if (point.time != null) {
		vi.spyOn(event, "timeStamp", "get").mockReturnValue(point.time);
	}
	return event;
}

type Point = { x: number; y: number; time?: number };

/** A touch as WebKit delivers one in the column: targeted at the root, pointer events first. */
function touchInColumn(path: Point[]): Event[] {
	const root = document.documentElement;
	const first = path[0];
	const last = path[path.length - 1];
	const events: Event[] = [
		pointer("pointerdown", first.x, first.y),
		touchAt("touchstart", first),
	];
	for (const point of path.slice(1)) {
		events.push(
			pointer("pointermove", point.x, point.y),
			touchAt("touchmove", point),
		);
	}
	events.push(pointer("pointerup", last.x, last.y), touchAt("touchend", last));
	for (const event of events) {
		root.dispatchEvent(event);
	}
	return events;
}

const columnProperty = () =>
	document.documentElement.style.getPropertyValue(IOS_COVER_COLUMN_PROPERTY);

describe("installIosCoverColumn", () => {
	let uninstall: (() => void) | null = null;
	let meta: HTMLMetaElement;
	let setDisplay: (display: Display) => void;

	beforeEach(() => {
		vi.stubGlobal("Touch", FakeTouch);
		vi.stubGlobal("PointerEvent", ViewlessPointerEvent);
		vi.stubGlobal("TouchEvent", ViewlessTouchEvent);
		vi.stubGlobal("MouseEvent", ViewlessMouseEvent);
		// jsdom stamps frames on a clock other than performance.now(), which the
		// glide measures against.
		vi.stubGlobal("requestAnimationFrame", (callback: FrameRequestCallback) =>
			window.setTimeout(() => callback(performance.now()), 16),
		);
		vi.stubGlobal("cancelAnimationFrame", (id: number) =>
			window.clearTimeout(id),
		);
		vi.stubGlobal("scrollX", 0);
		vi.stubGlobal("visualViewport", undefined);
		meta = document.createElement("meta");
		meta.name = "viewport";
		meta.content = DEVICE_WIDTH_META;
		document.head.appendChild(meta);
		setDisplay = emulateDuo(meta);
	});

	afterEach(() => {
		uninstall?.();
		uninstall = null;
		document.head.replaceChildren();
		document.body.replaceChildren();
		vi.unstubAllGlobals();
		vi.restoreAllMocks();
	});

	it("changes nothing outside a home-screen web app", () => {
		emulateDuo(meta, { standalone: false });
		uninstall = installIosCoverColumn();

		expect(meta.content).toBe(DEVICE_WIDTH_META);
		expect(columnProperty()).toBe("");
	});

	it("lays the page out across the whole cover display and restores it on cleanup", () => {
		uninstall = installIosCoverColumn();

		// The height too: a width alone keeps the screen's aspect ratio and
		// leaves room to scroll.
		expect(meta.content).toBe(
			"width=466, height=678, initial-scale=1, maximum-scale=1, user-scalable=no",
		);
		expect(columnProperty()).toBe("84px");

		uninstall();
		uninstall = null;
		expect(meta.content).toBe(DEVICE_WIDTH_META);
		expect(columnProperty()).toBe("");
	});

	it("gives the column back on the inner display and takes it again on the cover display", () => {
		uninstall = installIosCoverColumn();

		setDisplay(INNER);
		window.dispatchEvent(new window.Event("resize"));
		expect(meta.content).toBe(DEVICE_WIDTH_META);
		expect(columnProperty()).toBe("");

		setDisplay(COVER);
		window.dispatchEvent(new window.Event("resize"));
		expect(meta.content).toMatch(/^width=466, height=678,/);
		expect(columnProperty()).toBe("84px");
	});

	it("gives the column back when the unfold reaches the page after its window resize", () => {
		const visualViewport = new window.EventTarget();
		vi.stubGlobal("visualViewport", visualViewport);
		uninstall = installIosCoverColumn();

		window.dispatchEvent(new window.Event("resize"));
		setDisplay(INNER);
		visualViewport.dispatchEvent(new window.Event("resize"));

		expect(meta.content).toBe(DEVICE_WIDTH_META);
		expect(columnProperty()).toBe("");
	});

	describe("a sideways scroll of the page", () => {
		const panPage = () => {
			vi.stubGlobal("scrollX", 84);
			window.dispatchEvent(new window.Event("scroll"));
		};
		const layoutLog: string[] = [];
		let observer: MutationObserver;

		beforeEach(() => {
			uninstall = installIosCoverColumn();
			layoutLog.length = 0;
			observer = new window.MutationObserver(() =>
				layoutLog.push(meta.content),
			);
			observer.observe(meta, { attributes: true });
		});

		afterEach(() => observer.disconnect());

		// WebKit reveals the caret this way on every keystroke once typing
		// passes the column's edge.
		it("is scrolled back without laying the page out again when the page made it", async () => {
			vi.stubGlobal("scrollTo", (x: number) => vi.stubGlobal("scrollX", x));

			for (let keystroke = 0; keystroke < 3; keystroke++) {
				panPage();
				expect(window.scrollX).toBe(0);
				await frames(3);
			}

			await Promise.resolve();
			expect(layoutLog).toEqual([]);
		});

		it("is healed by laying the page out at device width when iOS holds it", async () => {
			vi.stubGlobal("scrollTo", () => undefined);

			panPage();
			await frames(2);
			expect(meta.content).toBe(DEVICE_WIDTH_META);

			vi.stubGlobal("scrollX", 0);
			await frames(3);
			expect(meta.content).toMatch(/^width=466, height=678,/);
		});

		it("stays at device width when the page goes away during a heal", async () => {
			vi.stubGlobal("scrollTo", () => undefined);

			panPage();
			await frames(1);
			uninstall?.();
			uninstall = null;
			await frames(3);

			expect(meta.content).toBe(DEVICE_WIDTH_META);
		});

		it("leaves the pan of a pinch zoom alone", () => {
			vi.stubGlobal("visualViewport", { scale: 2 });

			panPage();

			expect(meta.content).toMatch(/^width=466, height=678,/);
		});
	});

	describe("a touch in the column", () => {
		let button: HTMLButtonElement;
		let field: HTMLTextAreaElement;
		let hitTests: Array<{ x: number; y: number }>;

		beforeEach(() => {
			button = document.createElement("button");
			field = document.createElement("textarea");
			document.body.append(button, field);
			hitTests = [];
			// WebKit finds nothing past the visible width; the bridge hit-tests
			// with the page shifted left by the column, so the element under the
			// finger sits 84px further left.
			document.elementFromPoint = (x: number, y: number) => {
				hitTests.push({ x, y });
				if (x !== 430 - 84) {
					return null;
				}
				return y < 300 ? field : button;
			};
			uninstall = installIosCoverColumn();
		});

		it("clicks the element under the finger, and the page never sees the root as the target", () => {
			const seen: string[] = [];
			const record = (event: Event) =>
				seen.push(
					`${event.type} on ${event.target === button ? "button" : "something else"}`,
				);
			const types = [
				"pointerdown",
				"touchstart",
				"pointerup",
				"touchend",
				"click",
			];
			// Where React listens: the replayed events must bubble up to it.
			for (const type of types) {
				document.addEventListener(type, record);
			}

			const events = touchInColumn([{ x: 430, y: 600 }]);
			for (const type of types) {
				document.removeEventListener(type, record);
			}

			expect(hitTests).toContainEqual({ x: 346, y: 600 });
			expect(seen).toEqual(types.map((type) => `${type} on button`));
			// WebKit's own click for the tap would land somewhere else.
			expect(
				events.find((event) => event.type === "touchend")?.defaultPrevented,
			).toBe(true);
		});

		it("focuses a text field it taps", () => {
			touchInColumn([{ x: 430, y: 200 }]);

			expect(document.activeElement).toBe(field);
		});

		it.each([
			["clicks a tap that wobbles a little", { x: 433, y: 604 }, 1],
			["does not click when the finger moves away", { x: 430, y: 640 }, 0],
		])("%s", (_, end, expected) => {
			let clicks = 0;
			button.addEventListener("click", () => clicks++);

			touchInColumn([{ x: 430, y: 600 }, end]);

			expect(clicks).toBe(expected);
		});

		it("does not click what the status band hides", () => {
			let clicks = 0;
			button.addEventListener("click", () => clicks++);
			document.elementFromPoint = () => button;

			const events = touchInColumn([
				{ x: 430, y: IOS_STATUS_BAND_HEIGHT_PX - 1 },
			]);

			expect(clicks).toBe(0);
			expect(events.some((event) => event.defaultPrevented)).toBe(false);
		});

		it("does not click a press the browser cancelled", () => {
			let clicks = 0;
			button.addEventListener("click", () => clicks++);
			const root = document.documentElement;

			root.dispatchEvent(pointer("pointerdown", 430, 600));
			root.dispatchEvent(touchAt("touchstart", { x: 430, y: 600 }));
			root.dispatchEvent(pointer("pointercancel", 430, 600));
			root.dispatchEvent(touchAt("touchend", { x: 430, y: 600 }));

			expect(clicks).toBe(0);
		});

		it("scrolls what is under the finger, which iOS does not, and cancels the press", () => {
			const scroller = wrapInScroller(button, 300);
			const seen: string[] = [];
			for (const type of ["pointercancel", "click"]) {
				button.addEventListener(type, () => seen.push(type));
			}

			// Moving the finger down scrolls up, from the point where the touch
			// became a drag.
			touchInColumn([
				{ x: 430, y: 600 },
				{ x: 430, y: 615 },
				{ x: 430, y: 640 },
				{ x: 430, y: 660 },
			]);

			expect(scroller.scrollTop).toBeLessThanOrEqual(300 - 45);
			expect(seen).toEqual(["pointercancel"]);
		});

		it("keeps a flick gliding after the finger lifts, but not a slow drag", async () => {
			const scroller = wrapInScroller(button, 300);

			touchInColumn([
				{ x: 430, y: 600, time: 0 },
				{ x: 430, y: 630, time: 30 },
				{ x: 430, y: 660, time: 60 },
			]);
			const flickReleasedAt = scroller.scrollTop;
			await frames(3);
			expect(scroller.scrollTop).toBeLessThan(flickReleasedAt);

			touchInColumn([
				{ x: 430, y: 600, time: 1000 },
				{ x: 430, y: 630, time: 2000 },
				{ x: 430, y: 660, time: 3000 },
			]);
			const dragReleasedAt = scroller.scrollTop;
			await frames(3);
			expect(scroller.scrollTop).toBe(dragReleasedAt);
		});

		it("scrolls the scroller around one that is already at its end", () => {
			const inner = wrapInScroller(button, 0);
			const outer = wrapInScroller(inner, 300);

			touchInColumn([
				{ x: 430, y: 600 },
				{ x: 430, y: 640 },
				{ x: 430, y: 660 },
			]);

			expect(inner.scrollTop).toBe(0);
			expect(outer.scrollTop).toBeLessThan(300);
		});

		it("cancels the press instead of clicking when a second finger joins, as in a pinch", () => {
			const seen: string[] = [];
			for (const type of ["touchstart", "touchcancel", "touchend", "click"]) {
				button.addEventListener(type, (event) => {
					const touches =
						event instanceof TouchEvent ? event.touches.length : 0;
					seen.push(`${type}:${touches}`);
				});
			}
			const root = document.documentElement;
			const column = new window.Touch({
				identifier: 7,
				target: root,
				clientX: 430,
				clientY: 600,
			});
			const other = new window.Touch({
				identifier: 8,
				target: root,
				clientX: 200,
				clientY: 300,
			});
			const send = (type: string, touches: Touch[], changed: Touch) =>
				root.dispatchEvent(
					new window.TouchEvent(type, {
						bubbles: true,
						cancelable: true,
						touches,
						targetTouches: touches,
						changedTouches: [changed],
					}),
				);

			root.dispatchEvent(pointer("pointerdown", 430, 600));
			send("touchstart", [column], column);
			send("touchstart", [column, other], other);
			root.dispatchEvent(pointer("pointerup", 430, 600));
			send("touchend", [other], column);
			send("touchend", [], other);

			// A long-press timer that starts on touchstart stops on the touchcancel.
			expect(seen).toEqual(["touchstart:1", "touchcancel:0"]);
		});

		it("leaves touches left of the column to WebKit", () => {
			let clicks = 0;
			button.addEventListener("click", () => clicks++);

			const events = touchInColumn([{ x: 300, y: 600 }]);

			expect(hitTests).toEqual([]);
			expect(clicks).toBe(0);
			expect(events.some((event) => event.defaultPrevented)).toBe(false);
		});
	});

	describe("a drag left of the column", () => {
		let target: HTMLDivElement;

		beforeEach(() => {
			target = document.createElement("div");
			document.body.appendChild(target);
		});

		/** Sends a drag from (200, 300) through `moves`; returns whether the last move was prevented. */
		function drag(...moves: Array<[number, number]>): boolean {
			const send = (type: string, x: number, y: number) => {
				const event = touchAt(type, { x, y }, target);
				target.dispatchEvent(event);
				return event.defaultPrevented;
			};
			send("touchstart", 200, 300);
			let prevented = false;
			for (const [dx, dy] of moves) {
				prevented = send("touchmove", 200 + dx, 300 + dy);
			}
			return prevented;
		}

		it("is blocked when it would only pan the page sideways", () => {
			uninstall = installIosCoverColumn();

			expect(drag([-80, 4])).toBe(true);
		});

		it("is left to iOS when a tap drifts sideways", () => {
			uninstall = installIosCoverColumn();

			expect(drag([-8, 1])).toBe(false);
		});

		it("is left to iOS when it scrolls something", () => {
			wrapInScroller(target, 300);
			uninstall = installIosCoverColumn();

			expect(drag([4, -80])).toBe(false);
		});

		it("is left to iOS when a vertical scroll starts with a sideways wobble", () => {
			wrapInScroller(target, 300);
			uninstall = installIosCoverColumn();

			expect(drag([2, -1], [3, -40])).toBe(false);
		});

		it("is left to iOS on the inner display, which has no column", () => {
			uninstall = installIosCoverColumn();
			setDisplay(INNER);
			window.dispatchEvent(new window.Event("resize"));

			expect(drag([-80, 4])).toBe(false);
		});
	});
});
