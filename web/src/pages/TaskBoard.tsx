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
  FolderPlus,
  Layers,
  GripVertical,
} from 'lucide-react';
import {
  tasks as tasksApi,
  type Task,
  type TaskStatus,
  type TaskPriority,
  type TaskInput,
  type TaskBoardSet,
} from '@/api/client';
import { Button } from '@/components/ui/button';
import { Card, CardContent } from '@/components/ui/card';
import { Badge } from '@/components/ui/badge';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Textarea } from '@/components/ui/textarea';
import { Separator } from '@/components/ui/separator';
import { ConfirmDialog } from '@/components/ConfirmDialog';
import { InfoBanner } from '@/components/InfoBanner';
import { CollapsibleConnectionURLs } from '@/components/CollapsibleConnectionURLs';
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

const PRIORITY_LEVEL_OPTIONS: { value: number; label: string }[] = [
  { value: 1, label: 'P1 - Critical' },
  { value: 2, label: 'P2 - High' },
  { value: 3, label: 'P3 - Normal' },
  { value: 4, label: 'P4 - Low' },
  { value: 5, label: 'P5 - Backlog' },
];

const STATUS_META: Record<TaskStatus, { label: string; color: string; bg: string; border: string; dot: string }> = {
  todo:        { label: 'To Do',       color: 'text-gray-400',    bg: 'bg-gray-500/5',     border: 'border-gray-500/20',    dot: 'bg-gray-400' },
  in_progress: { label: 'In Progress',  color: 'text-blue-400',    bg: 'bg-blue-500/5',     border: 'border-blue-500/20',    dot: 'bg-blue-400' },
  done:        { label: 'Done',         color: 'text-emerald-400', bg: 'bg-emerald-500/5',  border: 'border-emerald-500/20', dot: 'bg-emerald-400' },
  blocked:     { label: 'Blocked',      color: 'text-red-400',    bg: 'bg-red-500/5',      border: 'border-red-500/20',     dot: 'bg-red-400' },
};

const PRIORITY_STYLES: Record<TaskPriority, string> = {
  low: 'bg-gray-500/15 text-gray-400 border-gray-500/30',
  medium: 'bg-yellow-500/15 text-yellow-400 border-yellow-500/30',
  high: 'bg-orange-500/15 text-orange-400 border-orange-500/30',
  urgent: 'bg-red-500/15 text-red-400 border-red-500/30',
};

const PRIORITY_LEVEL_STYLES: Record<number, string> = {
  1: 'bg-red-500/15 text-red-400 border-red-500/30',
  2: 'bg-orange-500/15 text-orange-400 border-orange-500/30',
  3: 'bg-blue-500/15 text-blue-400 border-blue-500/30',
  4: 'bg-gray-500/15 text-gray-400 border-gray-500/30',
  5: 'bg-gray-500/10 text-gray-500 border-gray-500/20',
};

const DEFAULT_BOARD_ID = 'default';

function StatusBadge({ status }: { status: TaskStatus }) {
  const meta = STATUS_META[status];
  return (
    <Badge variant="outline" className={cn('capitalize', meta.color)}>
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

function PriorityLevelBadge({ level }: { level: number }) {
  const style = PRIORITY_LEVEL_STYLES[level] ?? PRIORITY_LEVEL_STYLES[3];
  const label = PRIORITY_LEVEL_OPTIONS.find((o) => o.value === level)?.label ?? `P${level}`;
  return (
    <Badge variant="outline" className={cn('font-mono', style)}>
      {label}
    </Badge>
  );
}

export default function TaskBoard() {
  const queryClient = useQueryClient();
  const [selectedBoard, setSelectedBoard] = useState<string>(DEFAULT_BOARD_ID);
  const [statusFilter, setStatusFilter] = useState<TaskStatus | ''>('');
  const [priorityFilter, setPriorityFilter] = useState<TaskPriority | ''>('');
  const [editing, setEditing] = useState<Task | null>(null);
  const [createOpen, setCreateOpen] = useState(false);
  const [newBoardOpen, setNewBoardOpen] = useState(false);
  const [deleteBoardOpen, setDeleteBoardOpen] = useState(false);
  const [viewMode, setViewMode] = useState<'board' | 'list'>('board');

  const { data: boards } = useQuery({
    queryKey: ['task-board-sets'],
    queryFn: tasksApi.listSets,
  });

  const currentBoard = boards?.find((b) => b.id === selectedBoard);

  const { data: stats } = useQuery({
    queryKey: ['task-stats', selectedBoard],
    queryFn: () => tasksApi.stats(selectedBoard),
  });

  const { data: allTasks } = useQuery({
    queryKey: ['tasks', statusFilter, priorityFilter, selectedBoard],
    queryFn: () =>
      tasksApi.list(
        statusFilter || undefined,
        priorityFilter || undefined,
        selectedBoard,
      ),
  });

  const updateMutation = useMutation({
    mutationFn: ({ id, data }: { id: string; data: Partial<TaskInput> }) =>
      tasksApi.update(id, data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['tasks'] });
      queryClient.invalidateQueries({ queryKey: ['task-stats'] });
    },
  });

  const deleteMutation = useMutation({
    mutationFn: tasksApi.delete,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['tasks'] });
      queryClient.invalidateQueries({ queryKey: ['task-stats'] });
    },
  });

  const deleteBoardMutation = useMutation({
    mutationFn: tasksApi.deleteSet,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['task-board-sets'] });
      queryClient.invalidateQueries({ queryKey: ['tasks'] });
      queryClient.invalidateQueries({ queryKey: ['task-stats'] });
      setSelectedBoard(DEFAULT_BOARD_ID);
      setDeleteBoardOpen(false);
    },
  });

  const displayTasks = allTasks ?? [];
  const totalTasks = stats?.total ?? displayTasks.length;
  const completedPct = totalTasks > 0 ? Math.round(((stats?.done ?? 0) / totalTasks) * 100) : 0;
  const boardName = (id: string) =>
    id === DEFAULT_BOARD_ID ? 'Default' : boards?.find((b) => b.id === id)?.name ?? id;

  // Group tasks by status for kanban view
  const tasksByStatus: Record<TaskStatus, Task[]> = {
    todo: [],
    in_progress: [],
    done: [],
    blocked: [],
  };
  displayTasks.forEach((t) => {
    if (tasksByStatus[t.status]) tasksByStatus[t.status].push(t);
  });

  // Connection URLs for this task board
  const origin = typeof window !== 'undefined' ? window.location.origin : '';
  const taskServerId = selectedBoard === DEFAULT_BOARD_ID
    ? 'builtin-tasks'
    : `builtin-tasks:${selectedBoard}`;
  const mcpUrl = `${origin}/api/servers/${taskServerId}/mcp`;
  const sseUrl = `${origin}/api/servers/${taskServerId}/sse`;

  const handleStatusChange = (taskId: string, newStatus: TaskStatus) => {
    updateMutation.mutate({ id: taskId, data: { status: newStatus } });
  };

  return (
    <div className="space-y-5">
      {/* Header */}
      <div className="flex items-center justify-between gap-3">
        <div className="min-w-0">
          <h1 className="text-xl sm:text-2xl font-bold text-foreground">Task Board</h1>
          <p className="text-muted-foreground mt-1 text-sm">
            Kanban board for your projects — your AI agent can create and update tasks via MCP
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
            <TaskForm boardId={selectedBoard} onSaved={() => setCreateOpen(false)} />
          </DialogContent>
        </Dialog>
      </div>

      {/* Info banner */}
      <InfoBanner
        icon={KanbanSquare}
        title="What is the Task Board?"
        description="A persistent kanban board for project management tasks. Your AI agent can create, update, and query tasks through MCP — useful for tracking work across conversations."
        iconColor="text-orange-400"
        iconBg="bg-orange-500/10"
        tips={[
          { label: 'Status', explanation: 'Workflow state: To Do → In Progress → Done (or Blocked)' },
          { label: 'Priority', explanation: 'How urgent the task is: low, medium, high, or urgent' },
          { label: 'P-Level', explanation: 'Finer priority ranking from P1 (critical) to P5 (backlog)' },
          { label: 'Boards', explanation: 'Separate task boards for different projects — each board gets its own MCP endpoint' },
        ]}
      />

      {/* Collapsible connection URLs */}
      <CollapsibleConnectionURLs
        mcpUrl={mcpUrl}
        sseUrl={sseUrl}
        label="MCP Connection URLs"
        description={currentBoard && currentBoard.name !== 'tasks' ? `Board: ${currentBoard.name}` : undefined}
      />

      {/* Board selector + view toggle */}
      <div className="flex flex-wrap items-center gap-2">
        {boards && boards.length > 1 && (
          <div className="flex flex-wrap gap-2 items-center">
            <Layers className="size-4 text-muted-foreground shrink-0" />
            {boards.map((b) => (
              <button
                key={b.id}
                onClick={() => setSelectedBoard(b.id)}
                className={cn(
                  'px-3 py-1.5 rounded-lg text-sm font-medium transition-colors min-h-[36px] flex items-center gap-1.5',
                  selectedBoard === b.id
                    ? 'bg-primary text-primary-foreground'
                    : 'bg-muted text-muted-foreground hover:bg-accent hover:text-foreground'
                )}
              >
                {b.name}
                {b.is_default && <Badge variant="outline" className="text-[10px] py-0 px-1">default</Badge>}
              </button>
            ))}
          </div>
        )}
        <div className="ml-auto flex items-center gap-2">
          <Dialog open={newBoardOpen} onOpenChange={setNewBoardOpen}>
            <DialogTrigger
              render={
                <Button variant="outline" size="sm" className="shrink-0">
                  <FolderPlus className="size-4" />
                  <span className="hidden sm:inline">New Board</span>
                  <span className="sm:hidden">Board</span>
                </Button>
              }
            />
            <DialogContent className="sm:max-w-md">
              <NewBoardForm
                onSaved={(id) => {
                  setNewBoardOpen(false);
                  if (id) setSelectedBoard(id);
                }}
              />
            </DialogContent>
          </Dialog>
          {currentBoard && !currentBoard.is_default && (
            <Button
              variant="ghost"
              size="icon-sm"
              className="shrink-0 text-muted-foreground hover:text-destructive"
              onClick={() => setDeleteBoardOpen(true)}
              aria-label={`Delete board ${currentBoard.name}`}
            >
              <Trash2 className="size-4" />
            </Button>
          )}
          {/* View toggle */}
          <div className="flex items-center rounded-lg border border-border overflow-hidden shrink-0">
            <button
              onClick={() => setViewMode('board')}
              className={cn(
                'px-2.5 py-1.5 text-xs font-medium transition-colors',
                viewMode === 'board' ? 'bg-primary text-primary-foreground' : 'text-muted-foreground hover:bg-accent'
              )}
            >
              Board
            </button>
            <button
              onClick={() => setViewMode('list')}
              className={cn(
                'px-2.5 py-1.5 text-xs font-medium transition-colors',
                viewMode === 'list' ? 'bg-primary text-primary-foreground' : 'text-muted-foreground hover:bg-accent'
              )}
            >
              List
            </button>
          </div>
        </div>
      </div>

      {/* Stats summary + progress bar */}
      <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
        {STATUS_OPTIONS.map((s) => {
          const meta = STATUS_META[s];
          const count = s === 'todo' ? stats?.todo ?? 0
            : s === 'in_progress' ? stats?.in_progress ?? 0
            : s === 'done' ? stats?.done ?? 0
            : stats?.blocked ?? 0;
          return (
            <Card key={s} className={cn('border', meta.border)}>
              <CardContent className="p-4">
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-2">
                    <div className={cn('w-2 h-2 rounded-full', meta.dot)} />
                    <span className="text-xs text-muted-foreground">{meta.label}</span>
                  </div>
                  <span className={cn('text-2xl font-bold', meta.color)}>{count}</span>
                </div>
              </CardContent>
            </Card>
          );
        })}
      </div>

      {/* Progress bar */}
      {totalTasks > 0 && (
        <div className="flex items-center gap-3">
          <span className="text-xs text-muted-foreground shrink-0">Progress</span>
          <div className="flex-1 h-2 bg-muted rounded-full overflow-hidden">
            <div
              className="h-full bg-emerald-500 rounded-full transition-all"
              style={{ width: `${completedPct}%` }}
            />
          </div>
          <span className="text-xs font-medium text-foreground shrink-0">{completedPct}%</span>
        </div>
      )}

      {/* Filter pills: status + priority (list mode only) */}
      {viewMode === 'list' && (
        <>
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
        </>
      )}

      {/* Task display: kanban columns or flat list */}
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
      ) : viewMode === 'board' ? (
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-3">
          {STATUS_OPTIONS.map((status) => {
            const meta = STATUS_META[status];
            const columnTasks = tasksByStatus[status];
            return (
              <div key={status} className={cn('rounded-lg border p-3 space-y-3', meta.border, meta.bg)}>
                <div className="flex items-center justify-between px-1">
                  <div className="flex items-center gap-2">
                    <div className={cn('w-2 h-2 rounded-full', meta.dot)} />
                    <span className={cn('text-sm font-semibold', meta.color)}>{meta.label}</span>
                  </div>
                  <Badge variant="outline" className="text-xs">{columnTasks.length}</Badge>
                </div>
                <div className="space-y-2">
                  {columnTasks.map((task) => (
                    <TaskCard
                      key={task.id}
                      task={task}
                      boardName={task.board_id !== DEFAULT_BOARD_ID ? boardName(task.board_id) : undefined}
                      onDelete={() => deleteMutation.mutate(task.id)}
                      onEdit={() => setEditing(task)}
                      onStatusChange={(newStatus) => handleStatusChange(task.id, newStatus)}
                      compact
                    />
                  ))}
                  {columnTasks.length === 0 && (
                    <p className="text-xs text-muted-foreground/50 text-center py-4">No tasks</p>
                  )}
                </div>
              </div>
            );
          })}
        </div>
      ) : (
        <div className="space-y-3">
          {displayTasks.map((task) => (
            <TaskCard
              key={task.id}
              task={task}
              boardName={task.board_id !== DEFAULT_BOARD_ID ? boardName(task.board_id) : undefined}
              onDelete={() => deleteMutation.mutate(task.id)}
              onEdit={() => setEditing(task)}
              onStatusChange={(newStatus) => handleStatusChange(task.id, newStatus)}
            />
          ))}
        </div>
      )}

      {/* Edit dialog */}
      {editing && (
        <Dialog open onOpenChange={(open) => !open && setEditing(null)}>
          <DialogContent className="sm:max-w-2xl">
            <TaskForm
              task={editing}
              boardId={editing.board_id}
              onSaved={() => setEditing(null)}
            />
          </DialogContent>
        </Dialog>
      )}

      {/* Delete board confirmation */}
      <ConfirmDialog
        open={deleteBoardOpen}
        onOpenChange={setDeleteBoardOpen}
        title="Delete Board"
        description="Delete board"
        itemName={currentBoard?.name}
        confirmText="Delete Board"
        loading={deleteBoardMutation.isPending}
        onConfirm={() => {
          if (currentBoard) {
            deleteBoardMutation.mutate(currentBoard.id);
          }
        }}
      />
    </div>
  );
}

function TaskCard({
  task,
  boardName,
  onDelete,
  onEdit,
  onStatusChange,
  compact,
}: {
  task: Task;
  boardName?: string;
  onDelete: () => void;
  onEdit: () => void;
  onStatusChange: (status: TaskStatus) => void;
  compact?: boolean;
}) {
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [statusMenuOpen, setStatusMenuOpen] = useState(false);

  return (
    <>
      <Card className={cn(compact && 'hover:shadow-sm transition-shadow cursor-pointer')} >
        <CardContent className={cn('space-y-2', compact ? 'p-3' : 'space-y-3')}>
          {/* Top row: badges + actions */}
          <div className="flex items-start justify-between gap-2 flex-wrap">
            <div className="flex items-center gap-1.5 flex-wrap min-w-0">
              <PriorityBadge priority={task.priority} />
              <PriorityLevelBadge level={task.priority_level} />
              {boardName && (
                <Badge variant="outline" className="text-xs bg-purple-500/15 text-purple-400 border-purple-500/30">
                  {boardName}
                </Badge>
              )}
            </div>
            <div className="flex items-center gap-0.5 shrink-0">
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

          {/* Title */}
          <p className={cn('font-medium text-foreground', compact ? 'text-sm' : 'truncate')}>{task.title}</p>

          {/* Description (list mode only) */}
          {!compact && task.description && (
            <p className="text-sm text-muted-foreground whitespace-pre-wrap break-words">
              {task.description}
            </p>
          )}

          {/* Tags */}
          {task.tags.length > 0 && (
            <div className="flex items-center gap-1 flex-wrap">
              {task.tags.map((tag) => (
                <Badge key={tag} variant="outline" className="text-xs">
                  <Tag className="size-2.5 mr-0.5" />
                  {tag}
                </Badge>
              ))}
            </div>
          )}

          {/* Footer: metadata */}
          {!compact && <Separator />}
          <div className="flex items-center gap-3 text-xs text-muted-foreground flex-wrap">
            {!compact && (
              <span className="flex items-center gap-1">
                <Clock className="size-3" />
                {new Date(task.created_at).toLocaleDateString()}
              </span>
            )}
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

          {/* Quick status changer (board mode) */}
          {compact && (
            <div className="pt-1">
              <Select
                value={task.status}
                onValueChange={(val) => onStatusChange(val as TaskStatus)}
              >
                <SelectTrigger className="h-7 text-xs" >
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {STATUS_OPTIONS.map((s) => (
                    <SelectItem key={s} value={s} className="capitalize text-xs">
                      {s.replace('_', ' ')}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          )}
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
  boardId,
  onSaved,
}: {
  task?: Task;
  boardId: string;
  onSaved: () => void;
}) {
  const queryClient = useQueryClient();
  const [title, setTitle] = useState(task?.title ?? '');
  const [description, setDescription] = useState(task?.description ?? '');
  const [status, setStatus] = useState<TaskStatus>(task?.status ?? 'todo');
  const [priority, setPriority] = useState<TaskPriority>(task?.priority ?? 'medium');
  const [priorityLevel, setPriorityLevel] = useState<number>(task?.priority_level ?? 3);
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
        priority_level: priorityLevel,
        assignee,
        due_date: dueDate,
        tags: tagArray,
      };
      if (task) {
        return tasksApi.update(task.id, { ...payload, board_id: boardId });
      }
      return tasksApi.create({ ...payload, board_id: boardId });
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

        <div className="flex flex-col gap-1.5">
          <Label htmlFor="task-priority-level">Priority Level</Label>
          <Select
            value={String(priorityLevel)}
            onValueChange={(val) => setPriorityLevel(Number(val))}
          >
            <SelectTrigger className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {PRIORITY_LEVEL_OPTIONS.map((o) => (
                <SelectItem key={o.value} value={String(o.value)}>
                  {o.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
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

function NewBoardForm({ onSaved }: { onSaved: (id?: string) => void }) {
  const queryClient = useQueryClient();
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');

  const saveMutation = useMutation({
    mutationFn: () => tasksApi.createSet({ name, description }),
    onSuccess: (data) => {
      queryClient.invalidateQueries({ queryKey: ['task-board-sets'] });
      onSaved(data.id);
    },
  });

  return (
    <>
      <DialogHeader>
        <DialogTitle>New Task Board</DialogTitle>
        <DialogDescription>
          Create a new task board to organize tasks for a specific project or context.
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
          <Label htmlFor="board-name">Name</Label>
          <Input
            id="board-name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="Project Alpha"
            required
          />
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="board-description">Description (optional)</Label>
          <Input
            id="board-description"
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            placeholder="Tasks for the Alpha project"
          />
        </div>
        <DialogFooter>
          <DialogClose render={<Button type="button" variant="outline">Cancel</Button>} />
          <Button type="submit" disabled={saveMutation.isPending || !name}>
            {saveMutation.isPending ? 'Creating...' : 'Create Board'}
          </Button>
        </DialogFooter>
      </form>
    </>
  );
}
