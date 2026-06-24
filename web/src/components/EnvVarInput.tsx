import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Plus, Trash2, Variable } from 'lucide-react';
import { envVars as envVarsApi } from '@/api/client';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Badge } from '@/components/ui/badge';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectSeparator,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';

export interface EnvVarEntry {
  key: string;
  value: string; // literal value or ${KEY} reference
}

interface EnvVarInputProps {
  entries: EnvVarEntry[];
  onChange: (entries: EnvVarEntry[]) => void;
}

const REF_PATTERN = /^\$\{([A-Za-z_][A-Za-z0-9_]*)\}$/;

function isReference(value: string): boolean {
  return REF_PATTERN.test(value);
}

function refKey(value: string): string {
  const m = value.match(REF_PATTERN);
  return m ? m[1] : '';
}

export function EnvVarInput({ entries, onChange }: EnvVarInputProps) {
  const { data: envVars } = useQuery({
    queryKey: ['env-vars-all'],
    queryFn: () => envVarsApi.list(),
  });

  const availableKeys = (envVars || []).map((ev) => ev.key).sort();
  const [customMode, setCustomMode] = useState<Record<number, boolean>>({});

  const updateEntry = (index: number, field: 'key' | 'value', val: string) => {
    const next = [...entries];
    next[index] = { ...next[index], [field]: val };
    onChange(next);
  };

  const addEntry = () => {
    onChange([...entries, { key: '', value: '' }]);
  };

  const removeEntry = (index: number) => {
    onChange(entries.filter((_, i) => i !== index));
  };

  const switchToCustom = (index: number) => {
    setCustomMode((prev) => ({ ...prev, [index]: true }));
    updateEntry(index, 'value', '');
  };

  const switchToReference = (index: number, refValue: string) => {
    setCustomMode((prev) => ({ ...prev, [index]: false }));
    updateEntry(index, 'value', refValue);
  };

  return (
    <div className="space-y-2">
      {entries.map((entry, index) => {
        const isRef = !customMode[index] && isReference(entry.value);
        const showDropdown = !customMode[index] && availableKeys.length > 0;

        return (
          <div key={index} className="flex gap-2 items-start">
            {/* Key */}
            <Input
              placeholder="KEY"
              value={entry.key}
              onChange={(e) => updateEntry(index, 'key', e.target.value)}
              className="font-mono text-xs w-[40%] shrink-0"
            />

            {/* Value: reference badge + dropdown, or custom input */}
            <div className="flex gap-1.5 items-center flex-1 min-w-0">
              {isRef ? (
                <>
                  <Badge variant="secondary" className="font-mono text-xs shrink-0 gap-1">
                    <Variable className="size-3" />
                    {refKey(entry.value)}
                  </Badge>
                  <Select
                    value={entry.value}
                    onValueChange={(v) => {
                      if (!v) return;
                      if (v === '__custom__') {
                        switchToCustom(index);
                      } else {
                        updateEntry(index, 'value', v);
                      }
                    }}
                  >
                    <SelectTrigger size="sm" className="flex-1 min-w-0">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {availableKeys.map((key) => (
                        <SelectItem key={key} value={`\${${key}}`}>
                          <Variable className="size-3" /> {key}
                        </SelectItem>
                      ))}
                      <SelectSeparator />
                      <SelectItem value="__custom__">Custom value…</SelectItem>
                    </SelectContent>
                  </Select>
                </>
              ) : showDropdown && !customMode[index] && !entry.value ? (
                /* Empty value with available vars — show dropdown to pick or type custom */
                <Select
                  value=""
                  onValueChange={(v) => {
                    if (!v) return;
                    if (v === '__custom__') {
                      switchToCustom(index);
                    } else {
                      switchToReference(index, v);
                    }
                  }}
                >
                  <SelectTrigger size="sm" className="flex-1 min-w-0 text-muted-foreground">
                    <span className="flex items-center gap-1.5">
                      <Variable className="size-3" />
                      Select env var or custom…
                    </span>
                  </SelectTrigger>
                  <SelectContent>
                    {availableKeys.map((key) => (
                      <SelectItem key={key} value={`\${${key}}`}>
                        <Variable className="size-3" /> {key}
                      </SelectItem>
                    ))}
                    <SelectSeparator />
                    <SelectItem value="__custom__">Custom value…</SelectItem>
                  </SelectContent>
                </Select>
              ) : (
                /* Custom input mode */
                <Input
                  placeholder="value"
                  value={entry.value}
                  onChange={(e) => updateEntry(index, 'value', e.target.value)}
                  className="font-mono text-xs flex-1 min-w-0"
                />
              )}

              {/* Switch to reference dropdown (when in custom mode with a value) */}
              {customMode[index] && availableKeys.length > 0 && (
                <Select
                  value=""
                  onValueChange={(v) => {
                    if (!v || v === '__custom__') return;
                    switchToReference(index, v);
                  }}
                >
                  <SelectTrigger size="sm" className="w-8 px-0 justify-center shrink-0" title="Use env var reference">
                    <Variable className="size-3.5" />
                  </SelectTrigger>
                  <SelectContent>
                    {availableKeys.map((key) => (
                      <SelectItem key={key} value={`\${${key}}`}>
                        <Variable className="size-3" /> {key}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              )}
            </div>

            {/* Delete */}
            <Button
              type="button"
              variant="ghost"
              size="icon"
              className="size-7 shrink-0 text-muted-foreground hover:text-destructive"
              onClick={() => removeEntry(index)}
            >
              <Trash2 className="size-3" />
            </Button>
          </div>
        );
      })}

      <Button type="button" variant="outline" size="sm" onClick={addEntry}>
        <Plus className="size-3 mr-1" /> Add variable
      </Button>
    </div>
  );
}
