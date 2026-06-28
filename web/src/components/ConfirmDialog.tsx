import { Loader2, AlertTriangle } from 'lucide-react';
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
  DialogClose,
} from '@/components/ui/dialog';
import { Button } from '@/components/ui/button';

interface ConfirmDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  description?: string;
  confirmText?: string;
  cancelText?: string;
  onConfirm: () => void;
  destructive?: boolean;
  itemName?: string;
  loading?: boolean;
  error?: string;
}

/**
 * Reusable confirmation dialog for destructive actions.
 *
 * Renders a modal Dialog (not AlertDialog, which doesn't exist in this project)
 * with a warning icon, the item name highlighted in destructive color, and a
 * "cannot be undone" notice. The confirm button uses variant="destructive" by
 * default.
 *
 * The dialog stays open while `loading` is true — the parent is responsible for
 * closing it (via `onOpenChange`) once the mutation resolves.
 */
export function ConfirmDialog({
  open,
  onOpenChange,
  title,
  description,
  confirmText = 'Delete',
  cancelText = 'Cancel',
  onConfirm,
  destructive = true,
  itemName,
  loading = false,
   error,
  }: ConfirmDialogProps) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md" showCloseButton={false}>
        <DialogHeader>
          <div className="flex items-start gap-3">
            {destructive && (
              <div className="rounded-full bg-destructive/10 p-2 shrink-0 mt-0.5">
                <AlertTriangle className="size-5 text-destructive" />
              </div>
            )}
            <div className="flex-1 min-w-0">
              <DialogTitle>{title}</DialogTitle>
              <DialogDescription className="mt-1.5">
                {description && <span>{description} </span>}
                {itemName && (
                  <span className="font-semibold text-destructive break-words">
                    {itemName}
                  </span>
                )}
                {description || itemName ? '?' : ''}
              </DialogDescription>
            </div>
          </div>
        </DialogHeader>
        <div className="rounded-lg border border-destructive/30 bg-destructive/5 px-3 py-2 text-xs text-destructive">
          This action cannot be undone.
        </div>
         {error && (
           <div
             aria-live="polite"
             className="rounded-lg border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive break-words"
           >
             {error}
           </div>
         )}
        <DialogFooter>
          <DialogClose
            render={
              <Button type="button" variant="outline" disabled={loading}>
                {cancelText}
              </Button>
            }
          />
          <Button
            type="button"
            variant={destructive ? 'destructive' : 'default'}
            onClick={onConfirm}
            disabled={loading}
          >
            {loading && <Loader2 className="size-4 animate-spin" />}
            {confirmText}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
