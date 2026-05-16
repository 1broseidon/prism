import { toasts, dismiss } from "../state/toasts";

export function Toaster() {
  const list = toasts.value;
  if (list.length === 0) return null;
  return (
    <div class="toaster" role="status" aria-live="polite">
      {list.map((t) => (
        <button
          key={t.id}
          class="toast"
          onClick={() => dismiss(t.id)}
          title="dismiss"
        >
          <span class="toast-mark" />
          <span class="toast-message">{t.message}</span>
        </button>
      ))}
    </div>
  );
}
