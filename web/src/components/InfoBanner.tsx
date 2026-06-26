import { useState } from 'react';
import { type LucideIcon, ChevronDown, Info } from 'lucide-react';
import { cn } from '@/lib/utils';

interface InfoBannerProps {
  icon: LucideIcon;
  title: string;
  description: string;
  tips?: { label: string; explanation: string }[];
  iconColor?: string;
  iconBg?: string;
}

export function InfoBanner({
  icon: Icon,
  title,
  description,
  tips,
  iconColor = 'text-primary',
  iconBg = 'bg-primary/10',
}: InfoBannerProps) {
  const [expanded, setExpanded] = useState(false);

  return (
    <div className="rounded-lg border border-border bg-muted/30">
      <button
        onClick={() => setExpanded(!expanded)}
        className="w-full flex items-center gap-3 px-4 py-3 text-left"
      >
        <div className={cn('inline-flex items-center justify-center w-8 h-8 rounded-lg shrink-0', iconBg)}>
          <Icon className={cn('w-4 h-4', iconColor)} />
        </div>
        <div className="flex-1 min-w-0">
          <p className="text-sm font-medium text-foreground">{title}</p>
          <p className="text-xs text-muted-foreground mt-0.5 line-clamp-1">{description}</p>
        </div>
        <ChevronDown className={cn('size-4 text-muted-foreground shrink-0 transition-transform', expanded && 'rotate-180')} />
      </button>
      {expanded && (
        <div className="px-4 pb-3 pt-0 space-y-2">
          <p className="text-sm text-muted-foreground">{description}</p>
          {tips && tips.length > 0 && (
            <div className="space-y-1.5 mt-2">
              {tips.map((tip) => (
                <div key={tip.label} className="flex items-start gap-2 text-xs">
                  <span className="font-medium text-foreground shrink-0">{tip.label}:</span>
                  <span className="text-muted-foreground">{tip.explanation}</span>
                </div>
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  );
}
