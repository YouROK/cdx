import { useEffect, useRef, useState } from "react";

// Hint shows a "?" button with the full explanation. The popover opens on
// hover, on keyboard focus, and on a click, so it also works on touch screens
// where there is no hover.
export function Hint({ text, label = "More details" }) {
  const [hovered, setHovered] = useState(false);
  const [pinned, setPinned] = useState(false);
  const boxRef = useRef(null);
  const open = hovered || pinned;

  useEffect(() => {
    if (!pinned) return;
    function onPointerDown(event) {
      if (boxRef.current && !boxRef.current.contains(event.target)) setPinned(false);
    }
    function onKey(event) {
      if (event.key === "Escape") setPinned(false);
    }
    document.addEventListener("pointerdown", onPointerDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("pointerdown", onPointerDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [pinned]);

  return (
    <span className="hint" ref={boxRef}>
      <button
        type="button"
        className="hint-button"
        aria-label={label}
        aria-expanded={open}
        onClick={(event) => {
          event.preventDefault();
          event.stopPropagation();
          setPinned((current) => !current);
        }}
        onMouseEnter={() => setHovered(true)}
        onMouseLeave={() => setHovered(false)}
        onFocus={() => setHovered(true)}
        onBlur={() => setHovered(false)}
      >
        ?
      </button>
      {open ? (
        <span className="hint-pop" role="tooltip">
          {text}
        </span>
      ) : null}
    </span>
  );
}
