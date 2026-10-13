import { useEffect, useRef, type ReactNode, type TextareaHTMLAttributes } from 'react';

export const cx = (...c: (string | false | null | undefined)[]) => c.filter(Boolean).join(' ');

export function Badge({ kind, title, children }: { kind?: 'ok' | 'warn' | 'bad' | ''; title?: string; children: ReactNode }) {
  return <span className={cx('badge', kind)} title={title}>{children}</span>;
}

export const Dot = ({ kind }: { kind?: string }) => <span className={cx('dot', kind)} />;

export function Msg({ kind = 'info', children, style }: { kind?: 'ok' | 'bad' | 'info' | ''; children: ReactNode; style?: React.CSSProperties }) {
  return <div className={cx('msg', kind)} style={style}>{children}</div>;
}

/** A message slot: shows nothing until set. */
export type Note = { kind: 'ok' | 'bad' | 'info'; text: ReactNode } | null;
export const NoteView = ({ note }: { note: Note }) => (note ? <Msg kind={note.kind}>{note.text}</Msg> : null);

export function Card({ title, sub, actions, children }: { title?: ReactNode; sub?: ReactNode; actions?: ReactNode; children?: ReactNode }) {
  return (
    <div className="card">
      {(title || actions) && (
        <div className="card-head">
          <div>{title && <h2>{title}</h2>}{sub && <div className="sub">{sub}</div>}</div>
          {actions && <div className="actions">{actions}</div>}
        </div>
      )}
      {children}
    </div>
  );
}

export function Empty({ title, children }: { title?: string; children?: ReactNode }) {
  return <div className="empty">{title && <h3>{title}</h3>}{children}</div>;
}

export function Field({ label, hint, children, style }: { label: ReactNode; hint?: ReactNode; children: ReactNode; style?: React.CSSProperties }) {
  return (
    <label className="field" style={style}>
      <span>{label}</span>
      {children}
      {hint && <small>{hint}</small>}
    </label>
  );
}

export const Stat = ({ k, v }: { k: ReactNode; v: ReactNode }) => (
  <div className="stat"><div className="k">{k}</div><div className="v">{v}</div></div>
);

export function Tabs<T extends string>({ tabs, value, onChange }: { tabs: { id: T; label: ReactNode }[]; value: T; onChange: (t: T) => void }) {
  return (
    <div className="tabs">
      {tabs.map(t => <button key={t.id} type="button" className={value === t.id ? 'on' : ''} onClick={() => onChange(t.id)}>{t.label}</button>)}
    </div>
  );
}

/** A monospace editor where Tab indents instead of leaving the field. */
export function CodeEditor({ value, onChange, short, ...rest }: { value: string; onChange?: (v: string) => void; short?: boolean } & Omit<TextareaHTMLAttributes<HTMLTextAreaElement>, 'onChange' | 'value'>) {
  return (
    <textarea
      {...rest}
      className={cx(short && 'short', rest.className)}
      spellCheck={false}
      value={value}
      readOnly={!onChange || rest.readOnly}
      onChange={e => onChange?.(e.target.value)}
      onKeyDown={e => {
        if (e.key !== 'Tab' || !onChange || rest.readOnly) return;
        e.preventDefault();
        const t = e.currentTarget, s = t.selectionStart, end = t.selectionEnd;
        onChange(t.value.slice(0, s) + '  ' + t.value.slice(end));
        requestAnimationFrame(() => t.setSelectionRange(s + 2, s + 2));
      }}
    />
  );
}

/** Renders "+ "/"- " prefixed diff lines in color. */
export function DiffView({ diff }: { diff: string }) {
  return (
    <pre className="diff mono">
      {diff.split('\n').map((l, i) => (
        <span key={i} className={l.startsWith('+ ') ? 'add' : l.startsWith('- ') ? 'del' : undefined}>{l + '\n'}</span>
      ))}
    </pre>
  );
}

/** A native modal <dialog>, open while mounted. */
export function Modal({ onClose, children, wide }: { onClose: () => void; children: ReactNode; wide?: boolean }) {
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    const d = ref.current!;
    if (!d.open) d.showModal();
    return () => { if (d.open) d.close(); };
  }, []);
  return (
    <dialog ref={ref} onCancel={e => { e.preventDefault(); onClose(); }} style={wide ? { width: 'min(720px, calc(100vw - 32px))' } : undefined}>
      {children}
    </dialog>
  );
}
