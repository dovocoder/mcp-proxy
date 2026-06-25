import { useState } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import {
  Plus,
  Trash2,
  Clock,
  Pencil,
  KanbanSquare,
  User,
  Tag,
  Calendar,
} from 'lucide-react';
import {
  tasks as tasksApi,
  type Task,
  type TaskStatus,
  type TaskPriority,
  type TaskInput,
} from '@/api/client';
import { Button } from '@/components/ui/button';
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
  CardDescription,
} from '@/components/ui/card';
import { Badge } from '@/components/ui/badge';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Textarea } from '@/components/ui/textarea';
import { Separator } from '@/components/ui/separator';
import { ConfirmDialog } from '@/components/ConfirmDialog';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import {
  Dialog,
  DialogTrigger,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
  DialogClose,
} from '@/components/ui/dialog';
import { cn } from '@/lib/utils';

const STATUS_OPTIONS: TaskStatus[] = ['todo', 'in_progress', 'done', 'blocked'];
const PRIORITY_OPTIONS: TaskPriority[] = ['low', 'medium', 'high', 'urgent'];

const STATUS_STYLES: Record<TaskStatus, string> = {
  todo: 'bg-gray-500/15 text-gray-400 border-gray-500/30',
  in_progress: 'bg-blue-500/15 text-blue-400 border-blue-500/30',
  done: 'bg-emerald-500/15 text-emerald-400 border-emerald-500/30',
  blocked: 'bg-red-500/15 text-red-400 border-red-500/30',
};

const PRIORITY_STYLES: Record<TaskPriority, string> = {
  low: 'bg-gray-500/15 text-gray-400 border-gray-500/30',
  medium: 'bg-yellow-500/15 text-yellow-400 border-yellow-500/30',
  high: 'bg-orange-500/15 text-orange-400 border-orange-500/30',
  urgent: 'bg-red-500/15 text-red-400 border-red-500/30',
};

function StatusBadge({ status }: { status: TaskStatus }) {
  return (
    <Badge variant="outline" className={cn('capitalize', STATUS_STYLES[status])}>
      {status.replace('_', ' ')}
    </Badge>
  );
}

function PriorityBadge({ priority }: { priority: TaskPriority }) {
  return (
    <Badge variant="outline" className={cn('capitalize', PRIORITY_STYLES[priority])}>
      {priority}
    </Badge>
  );
}

export default function TaskBoard() {
  const queryClient = useQueryClient();
  const [statusFilter, setStatusFilter] = useState<TaskStatus | ''>('');
  const [priorityFilter, setPriorityFilter] = useState<TaskPriority | ''>('');
  const [editing, setEditing] = useState<Task | null>(null);
  const [createOpen, setCreateOpen] = useState(false);

  const { data: stats } = useQuery({
    queryKey: ['task-stats'],
    queryFn: tasksApi.stats,
  });

  const { data: allTasks } = useQuery({
    queryKey: ['tasks', statusFilter, priorityFilter],
    queryFn: () =>
      tasksApi.list(statusFilter || undefined, priorityFilter || undefined),
  });

  const deleteMutation = useMutation({
    mutationFn: tasksApi.delete,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['tasks'] });
      queryClient.invalidateQueries({ queryKey: ['task-stats'] });
    },
  });

  const displayTasks = allTasks ?? [];
  const totalTasks = stats?.total ?? displayTasks.length;

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between gap-3">
        <div className="min-w-0">
          <h1 className="text-xl sm:text-2xl font-bold text-foreground">Task Board</h1>
          <p className="text-muted-foreground mt-1 text-sm">
            Manage and track your tasks across statuses and priorities.
          </p>
        </div>
        <Dialog open={createOpen} onOpenChange={setCreateOpen}>
          <DialogTrigger
            render={
              <Button size="sm" className="shrink-0">
                <Plus className="size-4" />
                <span className="hidden sm:inline">New Task</span>
                <span className="sm:hidden">New</span>
              </Button>
            }
          />
          <DialogContent className="sm:max-w-2xl">
            <TaskForm onSaved={() => setCreateOpen(false)} />
          </DialogContent>
        </Dialog>
      </div>

      {/* Stats summary */}
      <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
        <StatCard label="To Do" value={stats?.todo ?? 0} className="text-gray-400" />
        <StatCard label="In Progress" value={stats?.in_progress ?? 0} className="text-blue-400" />
        <StatCard label="Done" value={stats?.done ?? 0} className="text-emerald-400" />
        <StatCard label="Blocked" value={stats?.blocked ?? 0} className="text-red-400" />
      </div>

      {/* Filter pills: status */}
      <div className="flex flex-wrap gap-2 items-center">
        <span className="text-xs text-muted-foreground mr-1">Status:</span>
        <button
          onClick={() => setStatusFilter('')}
          className={cn(
            'px-3 py-1.5 rounded-lg text-sm font-medium transition-colors min-h-[36px]',
            statusFilter === ''
              ? 'bg-primary text-primary-foreground'
              : 'bg-muted text-muted-foreground hover:bg-accent hover:text-foreground'
          )}
        >
          All
        </button>
        {STATUS_OPTIONS.map((s) => (
          <button
            key={s}
            onClick={() => setStatusFilter(s)}
            className={cn(
              'px-3 py-1.5 rounded-lg text-sm font-medium transition-colors min-h-[36px] capitalize',
              statusFilter === s
                ? 'bg-primary text-primary-foreground'
                : 'bg-muted text-muted-foreground hover:bg-accent hover:text-foreground'
            )}
          >
            {s.replace('_', ' ')}
          </button>
        ))}
      </div>

      {/* Filter pills: priority */}
      <div className="flex flex-wrap gap-2 items-center">
        <span className="text-xs text-muted-foreground mr-1">Priority:</span>
        <button
          onClick={() => setPriorityFilter('')}
          className={cn(
            'px-3 py-1.5 rounded-lg text-sm font-medium transition-colors min-h-[36px]',
            priorityFilter === ''
              ? 'bg-primary text-primary-foreground'
              : 'bg-muted text-muted-foreground hover:bg-accent hover:text-foreground'
          )}
        >
          All
        </button>
        {PRIORITY_OPTIONS.map((p) => (
          <button
            key={p}
            onClick={() => setPriorityFilter(p)}
            className={cn(
              'px-3 py-1.5 rounded-lg text-sm font-medium transition-colors min-h-[36px] capitalize',
              priorityFilter === p
                ? 'bg-primary text-primary-foreground'
                : 'bg-muted text-muted-foreground hover:bg-accent hover:text-foreground'
            )}
          >
            {p}
          </button>
        ))}
      </div>

      {/* Task list */}
      {displayTasks.length === 0 ? (
        <Card>
          <CardContent className="p-8 sm:p-12 text-center flex flex-col items-center gap-3">
            <KanbanSquare className="size-10 text-muted-foreground/50" />
            <p className="text-muted-foreground text-sm">
              {totalTasks === 0
                ? 'No tasks yet. Create your first task to get started.'
                : 'No tasks match the current filters.'}
            </p>
          </CardContent>
        </Card>
      ) : (
        <div className="space-y-3">
          {displayTasks.map((task) => (
            <TaskCard
              key={task.id}
              task={task}
              onDelete={() => deleteMutation.mutate(task.id)}
              onEdit={() => setEditing(task)}
            />
          ))}
        </div>
      )}

      {/* Edit dialog */}
      {editing && (
        <Dialog open onOpenChange={(open) => !open && setEditing(null)}>
          <DialogContent className="sm:max-w-2xl">
            <TaskForm task={editing} onSaved={() => setEditing(null)} />
          </DialogContent>
        </Dialog>
      )}
    </div>
  );
}

function StatCard({
  label,
  value,
  className,
}: {
  label: string;
  value: number;
  className?: string;
}) {
  return (
    <Card>
      <CardContent className="p-4">
        <div className="flex items-center justify-between">
          <span className="text-xs text-muted-foreground">{label}</span>
          <span className={cn('text-2xl font-bold', className)}>{value}</span>
        </div>
      </CardContent>
    </Card>
  );
}

function TaskCard({
  task,
  onDelete,
  onEdit,
}: {
  task: Task;
  onDelete: () => void;
  onEdit: () => void;
}) {
  const [deleteOpen, setDeleteOpen] = useState(false);

  return (
    <>
      <Card>
        <CardContent className="space-y-3">
          {/* Top row: title, status, priority, actions */}
          <div className="flex items-start justify-between gap-2 flex-wrap">
            <div className="flex items-center gap-2 flex-wrap min-w-0">
              <StatusBadge status={task.status} />
              <PriorityBadge priority={task.priority} />
              <span className="font-medium text-foreground truncate">{task.title}</span>
            </div>
            <div className="flex items-center gap-1 shrink-0">
              <Button variant="ghost" size="icon-sm" onClick={onEdit}>
                <Pencil className="size-3.5" />
              </Button>
              <Button
                variant="ghost"
                size="icon-sm"
                onClick={() => setDeleteOpen(true)}
                className="text-muted-foreground hover:text-destructive"
              >
                <Trash2 className="size-3.5" />
              </Button>
            </div>
          </div>

          {/* Description */}
          {task.description && (
            <p className="text-sm text-muted-foreground whitespace-pre-wrap break-words">
              {task.description}
            </p>
          )}

          {/* Tags */}
          {task.tags.length > 0 && (
            <div className="flex items-center gap-1.5 flex-wrap">
              {task.tags.map((tag) => (
                <Badge key={tag} variant="outline" className="text-xs">
                  <Tag className="size-2.5 mr-0.5" />
                  {tag}
                </Badge>
              ))}
            </div>
          )}

          {/* Footer: metadata */}
          <Separator />
          <div className="flex items-center gap-4 text-xs text-muted-foreground flex-wrap">
            <span className="flex items-center gap-1">
              <Clock className="size-3" />
              {new Date(task.created_at).toLocaleDateString()}
            </span>
            {task.assignee && (
              <span className="flex items-center gap-1">
                <User className="size-3" />
                {task.assignee}
              </span>
            )}
            {task.due_date && (
              <span className="flex items-center gap-1">
                <Calendar className="size-3" />
                {new Date(task.due_date).toLocaleDateString()}
              </span>
            )}
          </div>
        </CardContent>
      </Card>
      <ConfirmDialog
        open={deleteOpen}
        onOpenChange={setDeleteOpen}
        title="Delete Task"
        description="Delete"
        itemName={task.title}
        confirmText="Delete"
        onConfirm={() => {
          onDelete();
          setDeleteOpen(false);
        }}
      />
    </>
  );
}

function TaskForm({
  task,
  onSaved,
}: {
  task?: Task;
  onSaved: () => void;
}) {
  const queryClient = useQueryClient();
  const [title, setTitle] = useState(task?.title ?? '');
  const [description, setDescription] = useState(task?.description ?? '');
  const [status, setStatus] = useState<TaskStatus>(task?.status ?? 'todo');
  const [priority, setPriority] = useState<TaskPriority>(task?.priority ?? 'medium');
  const [assignee, setAssignee] = useState(task?.assignee ?? '');
  const [dueDate, setDueDate] = useState(task?.due_date ?? '');
  const [tags, setTags] = useState((task?.tags ?? []).join(', '));

  const saveMutation = useMutation({
    mutationFn: async () => {
      const tagArray = tags.split(',').map((t) => t.trim()).filter(Boolean);
      const payload: TaskInput = {
        title,
        description,
        status,
        priority,
        assignee,
        due_date: dueDate,
        tags: tagArray,
      };
      if (task) {
        return tasksApi.update(task.id, payload);
      }
      return tasksApi.create(payload);
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['tasks'] });
      queryClient.invalidateQueries({ queryKey: ['task-stats'] });
      onSaved();
    },
  });

  return (
    <>
      <DialogHeader>
        <DialogTitle>{task ? 'Edit Task' : 'New Task'}</DialogTitle>
        <DialogDescription>
          {task
            ? 'Update the task details.'
            : 'Create a new task on the board.'}
        </DialogDescription>
      </DialogHeader>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          saveMutation.mutate();
        }}
        className="flex flex-col gap-4"
      >
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="task-title">Title</Label>
          <Input
            id="task-title"
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            placeholder="Implement feature X"
            required
          />
        </div>

        <div className="grid grid-cols-2 gap-3">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="task-status">Status</Label>
            <Select value={status} onValueChange={(val) => setStatus(val as TaskStatus)}>
              <SelectTrigger className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {STATUS_OPTIONS.map((s) => (
                  <SelectItem key={s} value={s} className="capitalize">
                    {s.replace('_', ' ')}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="task-priority">Priority</Label>
            <Select value={priority} onValueChange={(val) => setPriority(val as TaskPriority)}>
              <SelectTrigger className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {PRIORITY_OPTIONS.map((p) => (
                  <SelectItem key={p} value={p} className="capitalize">
                    {p}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        </div>

        <div className="grid grid-cols-2 gap-3">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="task-assignee">Assignee (optional)</Label>
            <Input
              id="task-assignee"
              value={assignee}
              onChange={(e) => setAssignee(e.target.value)}
              placeholder="alex"
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="task-due-date">Due Date (optional)</Label>
            <Input
              id="task-due-date"
              type="date"
              value={dueDate ? dueDate.slice(0, 10) : ''}
              onChange={(e) => setDueDate(e.target.value)}
            />
          </div>
        </div>

        <div className="flex flex-col gap-1.5">
          <Label htmlFor="task-tags">Tags (comma-separated)</Label>
          <Input
            id="task-tags"
            value={tags}
            onChange={(e) => setTags(e.target.value)}
            placeholder="backend, urgent-fix"
          />
        </div>

        <div className="flex flex-col gap-1.5">
          <Label htmlFor="task-description">Description (optional)</Label>
          <Textarea
            id="task-description"
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            placeholder="Details about the task..."
            className="min-h-[100px]"
          />
        </div>

        <DialogFooter>
          <DialogClose render={<Button type="button" variant="outline">Cancel</Button>} />
          <Button type="submit" disabled={saveMutation.isPending || !title}>
            {saveMutation.isPending
              ? 'Saving...'
              : task
                ? 'Update Task'
                : 'Create Task'}
          </Button>
        </DialogFooter>
      </form>
    </>
  );
}
