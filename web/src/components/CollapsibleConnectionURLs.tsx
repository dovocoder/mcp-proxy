import { useState } from 'react';
import { Link2, Copy, Check, ChevronDown } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import { Separator } from '@/components/ui/separator';
import { cn } from '@/lib/utils';

interface CollapsibleConnectionURLsProps {
  mcpUrl: string;
  sseUrl: string;
  label: string;
  description?: string;
}

export function CollapsibleConnectionURLs({
  mcpUrl,
  sseUrl,
  label,
  description,
}: CollapsibleConnectionURLsProps) {
  const [expanded, setExpanded] = useState(false);
  const [copiedUrl, setCopiedUrl] = useState<string | null>(null);

  const copyToClipboard = (text: string) => {
    navigator.clipboard.writeText(text);
    setCopiedUrl(text);
    setTimeout(() => setCopiedUrl(null), 2000);
  };

  return (
    <div className="rounded-lg border border-border">
      <button
        onClick={() => setExpanded(!expanded)}
        className="w-full flex items-center gap-2 px-4 py-2.5 text-left hover:bg-accent/50 transition-colors"
      >
        <Link2 className="size-4 text-muted-foreground shrink-0" />
        <span className="text-sm font-medium text-foreground flex-1">{label}</span>
        {description && (
          <span className="text-xs text-muted-foreground hidden sm:inline">{description}</span>
        )}
        <ChevronDown className={cn('size-3.5 text-muted-foreground shrink-0 transition-transform', expanded && 'rotate-180')} />
      </button>
      {expanded && (
        <div className="px-4 pb-3 space-y-2.5">
          <Separator />
          <div className="flex items-center gap-2 pt-1">
            <Badge variant="default" className="font-mono shrink-0 text-xs">POST</Badge>
            <code className="flex-1 min-w-0 text-xs text-muted-foreground font-mono break-all">
              {mcpUrl}
            </code>
            <Button
              variant="outline"
              size="icon-sm"
              onClick={() => copyToClipboard(mcpUrl)}
              aria-label="Copy MCP URL"
              className="shrink-0"
            >
              {copiedUrl === mcpUrl ? <Check className="size-4" /> : <Copy className="size-4" />}
            </Button>
          </div>
          <div className="flex items-center gap-2">
            <Badge variant="secondary" className="font-mono shrink-0 text-xs bg-emerald-500/15 text-emerald-400">SSE</Badge>
            <code className="flex-1 min-w-0 text-xs text-muted-foreground font-mono break-all">
              {sseUrl}
            </code>
            <Button
              variant="outline"
              size="icon-sm"
              onClick={() => copyToClipboard(sseUrl)}
              aria-label="Copy SSE URL"
              className="shrink-0"
            >
              {copiedUrl === sseUrl ? <Check className="size-4" /> : <Copy className="size-4" />}
            </Button>
          </div>
        </div>
      )}
    </div>
  );
}
