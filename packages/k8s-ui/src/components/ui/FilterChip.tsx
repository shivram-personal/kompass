import { useEffect, useId, useMemo, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import { X } from 'lucide-react'
import { clsx } from 'clsx'

// FilterChip is a combobox-style filter primitive — a trigger button
// that opens a dropdown listbox for selection. Three modes:
//
//   single   — pick one value from a list of options
//   multi    — pick N values, checkbox list
//   text-add — type free-form tokens that accumulate as removable pills
//
// FilterChip is the "with-dropdown" pattern. For toggle pills (single
// button, no dropdown), use FilterPill instead. Both share the
// "filter UI" family but serve different selection patterns.
//
// Keyboard + a11y:
//   - Escape closes the dropdown + returns focus to trigger
//   - Trigger has role="combobox" + aria-expanded + aria-haspopup + aria-controls
//   - Listbox items have role="option" + aria-selected
//
// Multi-mode features:
//   - Per-option count badge (`Critical (12)`)
//   - Searchable (typeahead inside dropdown for 100+ option lists)
//   - groupBy (group options by a derived key, render section headers)
//   - tone (color the trigger when filter is active — `danger`, `warn`,
//     `ok`, `brand`, `neutral`)

export type FilterChipTone = 'neutral' | 'danger' | 'warn' | 'ok' | 'brand'

interface BaseProps {
  /** Label shown before the selection value on the trigger, e.g. "Kind:". */
  label: string
  /** Active-state color encoding (color when filter is set). Default: neutral. */
  tone?: FilterChipTone
  /** Optional classes on the trigger button. */
  className?: string
}

// ---- Single-select combobox: one value picked from a list. ----

interface OptionBase<T extends string> {
  value: T
  label: ReactNode
  /** Free-form annotation rendered after the label in tertiary text. */
  hint?: string
  /** Numeric count rendered as `(N)` after the label. Use for occurrence counts
   *  (e.g. "CrashLoopBackOff (12)" in a Problems filter). */
  count?: number
}

export interface FilterChipSingleProps<T extends string> extends BaseProps {
  mode: 'single'
  options: OptionBase<T>[]
  value: T
  onChange: (v: T) => void
}

// ---- Multi-select combobox: N values picked from a list. ----

export interface FilterChipMultiProps<T extends string> extends BaseProps {
  mode: 'multi'
  options: OptionBase<T>[]
  selected: T[]
  onChange: (next: T[]) => void
  /** Shown when `selected.length === 0`. */
  emptyLabel?: string
  /** Custom formatter for `selected.length === 1`. */
  singleLabel?: (value: T) => ReactNode
  /** Custom formatter for `selected.length > 1`. */
  manyLabel?: (count: number, total: number) => ReactNode
  /** Render a debounced search input at the top of the dropdown filtering
   *  options by label substring (case-insensitive). Useful when option count is large. */
  searchable?: boolean
  /** Group options by a derived key. Renders a small section header before each group. */
  groupBy?: (option: OptionBase<T>) => string
}

// ---- Text-add: user types free-form tokens that accumulate as chips. ----

export interface FilterChipTextAddProps extends BaseProps {
  mode: 'text-add'
  selected: string[]
  onAdd: (v: string) => void
  onRemove: (v: string) => void
  onClear: () => void
  placeholder?: string
  emptyLabel?: string
}

export type FilterChipProps<T extends string = string> =
  | FilterChipSingleProps<T>
  | FilterChipMultiProps<T>
  | FilterChipTextAddProps

const TONE_ACTIVE: Record<FilterChipTone, string> = {
  neutral: 'border-theme-border bg-theme-surface hover:bg-theme-hover',
  danger: 'border-rose-200 bg-rose-50 text-rose-700 dark:border-rose-900 dark:bg-rose-950/40 dark:text-rose-300',
  warn: 'border-amber-200 bg-amber-50 text-amber-800 dark:border-amber-900 dark:bg-amber-950/40 dark:text-amber-300',
  ok: 'border-emerald-200 bg-emerald-50 text-emerald-800 dark:border-emerald-900 dark:bg-emerald-950/40 dark:text-emerald-200',
  brand: 'border-[var(--color-radar-accent)] bg-[var(--color-brand-50)] dark:bg-[var(--color-brand-950)]',
}

function isFilterActive<T extends string>(props: FilterChipProps<T>): boolean {
  if (props.mode === 'single') return false  // single-mode always has a value, not "active/inactive"
  return props.selected.length > 0
}

export function FilterChip<T extends string = string>(props: FilterChipProps<T>) {
  const [open, setOpen] = useState(false)
  const triggerRef = useRef<HTMLButtonElement>(null)
  const listboxId = useId()
  const tone = props.tone ?? 'neutral'
  const active = isFilterActive(props)

  useEffect(() => {
    if (!open) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        setOpen(false)
        triggerRef.current?.focus()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open])

  const displayLabel = computeTriggerLabel(props)

  return (
    <div className="relative">
      <button
        ref={triggerRef}
        type="button"
        onClick={() => setOpen((x) => !x)}
        role="combobox"
        aria-expanded={open}
        aria-haspopup="listbox"
        aria-controls={listboxId}
        className={clsx(
          'inline-flex items-center gap-2 rounded-md border px-3 py-1.5 text-sm transition-colors',
          // Tone applies only when filter is active. Inactive always uses neutral
          // chrome so the trigger doesn't shout color until the user has set a filter.
          active ? TONE_ACTIVE[tone] : TONE_ACTIVE.neutral,
          props.className,
        )}
      >
        <span className={active ? 'opacity-80' : 'text-theme-text-tertiary'}>{props.label}</span>
        <span className={active ? 'font-medium' : 'font-medium text-theme-text-primary'}>
          {displayLabel}
        </span>
      </button>
      {open && (
        <>
          <div
            className="fixed inset-0 z-10"
            onClick={() => setOpen(false)}
            aria-hidden
          />
          <div
            id={listboxId}
            role="listbox"
            className="absolute left-0 top-full z-20 mt-1 max-h-96 w-72 overflow-y-auto rounded-md border border-theme-border bg-theme-base shadow-lg"
          >
            {renderBody(props, () => setOpen(false))}
          </div>
        </>
      )}
    </div>
  )
}

function computeTriggerLabel<T extends string>(props: FilterChipProps<T>): ReactNode {
  if (props.mode === 'single') {
    const picked = props.options.find((o) => o.value === props.value)
    return picked?.label ?? props.value
  }
  if (props.mode === 'multi') {
    if (props.selected.length === 0) return props.emptyLabel ?? 'Any'
    if (props.selected.length === 1) {
      if (props.singleLabel) return props.singleLabel(props.selected[0])
      const picked = props.options.find((o) => o.value === props.selected[0])
      return picked?.label ?? props.selected[0]
    }
    if (props.manyLabel) return props.manyLabel(props.selected.length, props.options.length)
    return `${props.selected.length} of ${props.options.length}`
  }
  // text-add
  if (props.selected.length === 0) return props.emptyLabel ?? 'Any'
  if (props.selected.length === 1) return props.selected[0]
  return `${props.selected.length} items`
}

function renderBody<T extends string>(
  props: FilterChipProps<T>,
  close: () => void,
): ReactNode {
  if (props.mode === 'single') return <SingleBody<T> {...props} close={close} />
  if (props.mode === 'multi') return <MultiBody<T> {...props} close={close} />
  return <TextAddBody {...props} />
}

function OptionLabel<T extends string>({ opt }: { opt: OptionBase<T> }) {
  return (
    <>
      <span className="truncate text-theme-text-primary">{opt.label}</span>
      {opt.count !== undefined && (
        <span className="ml-auto text-xs text-theme-text-tertiary">({opt.count})</span>
      )}
      {opt.hint && (
        <span className={clsx('text-xs text-theme-text-tertiary', opt.count !== undefined ? 'ml-1' : 'ml-auto')}>
          {opt.hint}
        </span>
      )}
    </>
  )
}

function SingleBody<T extends string>(props: FilterChipSingleProps<T> & { close: () => void }) {
  return (
    <>
      {props.options.map((opt) => (
        <button
          key={opt.value}
          type="button"
          role="option"
          aria-selected={opt.value === props.value}
          onClick={() => {
            props.onChange(opt.value)
            props.close()
          }}
          className={clsx(
            'flex w-full items-center gap-2 px-3 py-2 text-left text-sm hover:bg-theme-hover',
            opt.value === props.value && 'bg-[var(--color-brand-50)] dark:bg-[var(--color-brand-950)]',
          )}
        >
          <OptionLabel opt={opt} />
        </button>
      ))}
    </>
  )
}

function MultiBody<T extends string>(props: FilterChipMultiProps<T> & { close: () => void }) {
  const [search, setSearch] = useState('')

  const visibleOptions = useMemo(() => {
    if (!props.searchable || !search) return props.options
    const s = search.toLowerCase()
    return props.options.filter((o) => {
      // Match against the string representation of the label. ReactNode
      // labels that aren't strings just match against value.
      const label = typeof o.label === 'string' ? o.label.toLowerCase() : ''
      return label.includes(s) || o.value.toLowerCase().includes(s)
    })
  }, [props.options, props.searchable, search])

  const grouped = useMemo(() => {
    if (!props.groupBy) return null
    const groupBy = props.groupBy
    const map = new Map<string, OptionBase<T>[]>()
    for (const opt of visibleOptions) {
      const key = groupBy(opt)
      const arr = map.get(key) ?? []
      arr.push(opt)
      map.set(key, arr)
    }
    return Array.from(map.entries())
  }, [visibleOptions, props.groupBy])

  const renderOption = (opt: OptionBase<T>) => (
    <label
      key={opt.value}
      role="option"
      aria-selected={props.selected.includes(opt.value)}
      className="flex cursor-pointer items-center gap-2 px-3 py-2 text-sm hover:bg-theme-hover"
    >
      <input
        type="checkbox"
        checked={props.selected.includes(opt.value)}
        onChange={() => {
          const next = props.selected.includes(opt.value)
            ? props.selected.filter((v) => v !== opt.value)
            : [...props.selected, opt.value]
          props.onChange(next)
        }}
      />
      <OptionLabel opt={opt} />
    </label>
  )

  return (
    <>
      {props.searchable && (
        <div className="sticky top-0 border-b border-theme-border bg-theme-base p-2">
          <input
            type="text"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="Search…"
            className="w-full rounded-md border border-theme-border bg-theme-base px-2 py-1 text-xs focus:border-[var(--color-radar-accent)] focus:outline-none"
            autoFocus
          />
        </div>
      )}
      {visibleOptions.length === 0 ? (
        <div className="px-3 py-2 text-sm text-theme-text-secondary">
          {props.options.length === 0 ? 'No options' : 'No matches'}
        </div>
      ) : grouped ? (
        grouped.map(([groupKey, opts]) => (
          <div key={groupKey}>
            <div className="border-b border-theme-border bg-theme-surface px-3 py-1 text-[10px] font-medium uppercase tracking-wide text-theme-text-tertiary">
              {groupKey}
            </div>
            {opts.map(renderOption)}
          </div>
        ))
      ) : (
        visibleOptions.map(renderOption)
      )}
      {props.selected.length > 0 && (
        <button
          type="button"
          onClick={() => {
            props.onChange([])
            props.close()
          }}
          className="flex w-full items-center gap-1 border-t border-theme-border px-3 py-2 text-left text-xs text-theme-text-secondary hover:bg-theme-hover"
        >
          <X className="h-3 w-3" aria-hidden /> Clear
        </button>
      )}
    </>
  )
}

function TextAddBody(props: FilterChipTextAddProps) {
  const [text, setText] = useState('')
  return (
    <div className="p-2">
      <div className="flex gap-1">
        <input
          type="text"
          value={text}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && text.trim()) {
              props.onAdd(text.trim())
              setText('')
            }
          }}
          placeholder={props.placeholder ?? ''}
          className="flex-1 rounded-md border border-theme-border bg-theme-base px-2 py-1 text-sm focus:border-[var(--color-radar-accent)] focus:outline-none"
        />
        <button
          type="button"
          disabled={!text.trim()}
          onClick={() => {
            props.onAdd(text.trim())
            setText('')
          }}
          className="rounded-md bg-[var(--color-radar-accent)] px-2 py-1 text-xs text-white disabled:opacity-50"
        >
          Add
        </button>
      </div>
      {props.selected.length > 0 && (
        <div className="mt-2 flex flex-wrap gap-1">
          {props.selected.map((v) => (
            <span
              key={v}
              className="inline-flex items-center gap-1 rounded-full bg-[var(--color-brand-50)] px-2 py-0.5 text-xs dark:bg-[var(--color-brand-950)]"
            >
              <span className="text-theme-text-primary">{v}</span>
              <button
                type="button"
                onClick={() => props.onRemove(v)}
                className="text-theme-text-tertiary hover:text-theme-text-primary"
                aria-label={`Remove ${v}`}
              >
                <X className="h-3 w-3" aria-hidden />
              </button>
            </span>
          ))}
          <button
            type="button"
            onClick={props.onClear}
            className="text-xs text-theme-text-secondary hover:text-theme-text-primary"
          >
            clear
          </button>
        </div>
      )}
    </div>
  )
}
