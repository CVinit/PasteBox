import {
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
  type CSSProperties,
  type KeyboardEvent,
} from "react";
import { createPortal } from "react-dom";
import { Check, ChevronDown } from "lucide-react";

type Option<T extends string> = {
  value: T;
  label: string;
  disabled?: boolean;
};

export function AdminSelect<T extends string>({
  label,
  value,
  options,
  onChange,
  disabled = false,
}: {
  label: string;
  value: T;
  options: readonly Option<T>[];
  onChange: (value: T) => void;
  disabled?: boolean;
}) {
  const id = useId();
  const trigger = useRef<HTMLButtonElement>(null);
  const list = useRef<HTMLDivElement>(null);
  const search = useRef({ text: "", time: 0 });
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(-1);
  const [position, setPosition] = useState<CSSProperties>({});
  const selected = options.findIndex((option) => option.value === value);
  const unavailable = disabled || !options.some((option) => !option.disabled);
  const expanded = open && !unavailable;

  function show(index = selected) {
    if (unavailable) return;
    search.current = { text: "", time: 0 };
    setActive(
      index >= 0 && !options[index]?.disabled
        ? index
        : options.findIndex((option) => !option.disabled),
    );
    setOpen(true);
  }

  function choose(index: number) {
    const option = options[index];
    if (!option || option.disabled || unavailable) return;
    onChange(option.value);
    setOpen(false);
    trigger.current?.focus({ preventScroll: true });
  }

  function onKeyDown(event: KeyboardEvent<HTMLButtonElement>) {
    const enabled = options.flatMap((option, index) =>
      option.disabled ? [] : [index],
    );
    if (unavailable) return;
    switch (event.key) {
      case "ArrowDown":
      case "ArrowUp": {
        event.preventDefault();
        if (!expanded) {
          show(
            selected >= 0
              ? selected
              : event.key === "ArrowUp"
                ? enabled.at(-1)
                : enabled[0],
          );
          return;
        }
        const step = event.key === "ArrowDown" ? 1 : -1;
        const index = enabled.indexOf(active);
        setActive(enabled[(index + step + enabled.length) % enabled.length]);
        return;
      }
      case "Home":
      case "End":
        event.preventDefault();
        if (!expanded) show();
        setActive(
          event.key === "Home" ? enabled[0] : enabled[enabled.length - 1],
        );
        return;
      case "Enter":
      case " ":
        event.preventDefault();
        if (expanded) choose(active);
        else show();
        return;
      case "Escape":
        if (expanded) {
          event.preventDefault();
          event.stopPropagation();
          setOpen(false);
        }
        return;
      case "Tab":
        setOpen(false);
        return;
    }
    if (
      event.key.length !== 1 ||
      event.ctrlKey ||
      event.metaKey ||
      event.altKey
    )
      return;
    event.preventDefault();
    const now = Date.now();
    const text =
      (now - search.current.time < 700 ? search.current.text : "") +
      event.key.toLocaleLowerCase();
    const query = [...text].every((character) => character === text[0])
      ? text[0]
      : text;
    const start = expanded ? active : selected;
    const match = options.findIndex((_, offset) => {
      const index = (start + 1 + offset + options.length) % options.length;
      return (
        !options[index].disabled &&
        options[index].label.toLocaleLowerCase().startsWith(query)
      );
    });
    if (!expanded) show();
    search.current = { text, time: now };
    if (match >= 0)
      setActive((start + 1 + match + options.length) % options.length);
  }

  useEffect(() => {
    if (unavailable) setOpen(false);
  }, [unavailable]);

  useLayoutEffect(() => {
    if (!expanded) return;
    function place() {
      const rect = trigger.current?.getBoundingClientRect();
      if (!rect) return;
      const viewportWidth = document.documentElement.clientWidth;
      const viewportHeight = window.innerHeight;
      if (rect.bottom < 0 || rect.top > viewportHeight) {
        setOpen(false);
        return;
      }
      const gap = 6;
      const below = viewportHeight - rect.bottom - gap - 8;
      const above = rect.top - gap - 8;
      const desired = Math.min(list.current?.scrollHeight ?? 280, 280);
      const upwards = below < desired && above > below;
      const maxHeight = Math.max(0, Math.min(280, upwards ? above : below));
      const width = Math.min(Math.max(rect.width, 160), viewportWidth - 16);
      setPosition({
        left: Math.max(8, Math.min(rect.left, viewportWidth - width - 8)),
        top: upwards
          ? rect.top - gap - Math.min(desired, maxHeight)
          : rect.bottom + gap,
        width,
        maxHeight,
      });
    }
    place();
    const observer = new ResizeObserver(place);
    if (trigger.current) observer.observe(trigger.current);
    window.addEventListener("resize", place);
    window.addEventListener("scroll", place, true);
    return () => {
      observer.disconnect();
      window.removeEventListener("resize", place);
      window.removeEventListener("scroll", place, true);
    };
  }, [expanded, options]);

  useEffect(() => {
    if (!expanded) return;
    list.current
      ?.querySelectorAll<HTMLElement>('[role="option"]')
      [active]?.scrollIntoView({ block: "nearest" });
  }, [expanded, active]);

  useEffect(() => {
    if (!expanded) return;
    function closeOutside(event: PointerEvent) {
      if (
        event.target instanceof Node &&
        !trigger.current?.contains(event.target) &&
        !list.current?.contains(event.target)
      ) {
        setOpen(false);
      }
    }
    document.addEventListener("pointerdown", closeOutside);
    return () => document.removeEventListener("pointerdown", closeOutside);
  }, [expanded]);

  return (
    <div className="admin-select">
      <button
        ref={trigger}
        type="button"
        className="admin-select-trigger"
        role="combobox"
        aria-label={label}
        aria-expanded={expanded}
        aria-haspopup="listbox"
        aria-controls={expanded ? id : undefined}
        aria-activedescendant={
          expanded && active >= 0 ? `${id}-${active}` : undefined
        }
        disabled={unavailable}
        onClick={() => (expanded ? setOpen(false) : show())}
        onKeyDown={onKeyDown}
        onBlur={() => setOpen(false)}
      >
        <span>{options[selected]?.label ?? value}</span>
        <ChevronDown size={16} aria-hidden="true" />
      </button>
      {expanded &&
        createPortal(
          <div
            ref={list}
            id={id}
            className="admin-select-menu"
            role="listbox"
            aria-label={label}
            style={position}
            onPointerDown={(event) => event.preventDefault()}
          >
            {options.map((option, index) => (
              <div
                key={option.value}
                id={`${id}-${index}`}
                role="option"
                aria-selected={option.value === value}
                aria-disabled={option.disabled || undefined}
                className={`admin-select-option${index === active ? " admin-select-option--active" : ""}`}
                onPointerMove={() => {
                  if (!option.disabled) setActive(index);
                }}
                onClick={() => choose(index)}
              >
                <span>{option.label}</span>
                {option.value === value && (
                  <Check size={16} aria-hidden="true" />
                )}
              </div>
            ))}
          </div>,
          document.body,
        )}
    </div>
  );
}
