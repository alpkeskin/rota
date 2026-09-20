"use client"

import * as React from "react"
import { X } from "lucide-react"
import { Input } from "@/components/ui/input"
import { cn } from "@/lib/utils"

interface TagInputProps {
  value: string[]
  onChange: (tags: string[]) => void
  /** Known tags shown as one-click suggestions (already-added ones are hidden) */
  suggestions?: string[]
  placeholder?: string
  disabled?: boolean
  id?: string
}

const chip =
  "border-border inline-flex items-center gap-1 rounded border px-1.5 py-0.5 text-[0.6875rem] leading-4 font-medium"

// Chips-style tag editor: Enter/comma adds, × or Backspace on empty removes.
export function TagInput({
  value,
  onChange,
  suggestions = [],
  placeholder = "Add tag…",
  disabled,
  id,
}: TagInputProps) {
  const [draft, setDraft] = React.useState("")

  const addTag = (raw: string) => {
    const tag = raw.trim()
    if (!tag || value.includes(tag)) return
    onChange([...value, tag])
  }

  const removeTag = (tag: string) => onChange(value.filter((t) => t !== tag))

  const commitDraft = () => {
    if (draft.trim()) {
      addTag(draft)
      setDraft("")
    }
  }

  const available = suggestions.filter((s) => !value.includes(s))

  return (
    <div className="flex flex-col gap-1.5">
      {value.length > 0 && (
        <div className="flex flex-wrap gap-1">
          {value.map((tag) => (
            <span key={tag} className={cn(chip, "pr-0.5")}>
              {tag}
              <button
                type="button"
                className="text-muted-foreground hover:text-foreground rounded-sm p-0.5 focus-visible:ring-ring focus-visible:ring-2 focus-visible:outline-none"
                onClick={() => removeTag(tag)}
                disabled={disabled}
                aria-label={`Remove ${tag}`}
              >
                <X className="size-3" aria-hidden />
              </button>
            </span>
          ))}
        </div>
      )}
      <Input
        id={id}
        value={draft}
        placeholder={placeholder}
        disabled={disabled}
        onChange={(e) => setDraft(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter" || e.key === ",") {
            e.preventDefault()
            commitDraft()
          } else if (e.key === "Backspace" && !draft && value.length > 0) {
            removeTag(value[value.length - 1])
          }
        }}
        onBlur={commitDraft}
      />
      {available.length > 0 && (
        <div className="flex flex-wrap gap-1">
          {available.slice(0, 12).map((s) => (
            <button
              key={s}
              type="button"
              disabled={disabled}
              className={cn(chip, "text-muted-foreground hover:bg-accent hover:text-foreground transition-colors focus-visible:ring-ring focus-visible:ring-2 focus-visible:outline-none")}
              onClick={() => addTag(s)}
            >
              {s}
            </button>
          ))}
        </div>
      )}
    </div>
  )
}
