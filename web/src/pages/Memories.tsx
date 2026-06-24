import { useState } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { Plus, Trash2, Search, Brain, Clock, Eye, Pencil, X, Check, FolderPlus, Layers } from 'lucide-react';
import { memories as memApi, memorySets as setsApi, type Memory, type MemorySet } from '@/api/client';
import { Button } from '@/components/ui/button';
import { Card, CardContent } from '@/components/ui/card';
import { Badge } from '@/components/ui/badge';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Textarea } from '@/components/ui/textarea';
import { Separator } from '@/components/ui/separator';
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

export default function Memories() {
  const queryClient = useQueryClient();
  const [selectedSet, setSelectedSet] = useState<string>('default');
  const [selectedPalace, setSelectedPalace] = useState<string>('');
  const [searchQuery, setSearchQuery] = useState('');
  const [searchResults, setSearchResults] = useState<Memory[] | null>(null);
  const [editing, setEditing] = useState<Memory | null>(null);
  const [createOpen, setCreateOpen] = useState(false);
  const [newSetOpen, setNewSetOpen] = useState(false);

  const { data: sets } = useQuery({ queryKey: ['memory-sets'], queryFn: setsApi.list });

  const { data: allMemories } = useQuery({
    queryKey: ['memories', selectedSet, selectedPalace],
    queryFn: () => memApi.list(selectedSet, selectedPalace || undefined),
  });

  const { data: palaces } = useQuery({
    queryKey: ['palaces', selectedSet],
    queryFn: () => memApi.palaces(selectedSet),
  });

  const deleteMutation = useMutation({
    mutationFn: memApi.delete,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['memories', selectedSet] });
      queryClient.invalidateQueries({ queryKey: ['palaces', selectedSet] });
    },
  });

  const deleteSetMutation = useMutation({
    mutationFn: setsApi.delete,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['memory-sets'] });
      setSelectedSet('default');
    },
  });

  const handleSearch = async () => {
    if (!searchQuery.trim()) {
      setSearchResults(null);
      return;
    }
    const results = await memApi.search(selectedSet, searchQuery);
    setSearchResults(results);
  };

  const displayMemories = searchResults ?? allMemories ?? [];
  const palaceList = palaces ?? [];
  const totalMemories = palaceList.reduce((sum, p) => sum + p.count, 0);
  const currentSet = sets?.find((s) => s.id === selectedSet);

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between gap-3">
        <div className="min-w-0">
          <h1 className="text-xl sm:text-2xl font-bold text-foreground">Memories</h1>
          <p className="text-muted-foreground mt-1 text-sm">
            {currentSet?.description || `Memory set: ${currentSet?.name ?? 'Default'}`}
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Dialog open={newSetOpen} onOpenChange={setNewSetOpen}>
            <DialogTrigger
              render={
                <Button variant="outline" size="sm" className="shrink-0">
                  <FolderPlus className="size-4" />
                  <span className="hidden sm:inline">New Set</span>
                </Button>
              }
            />
            <DialogContent className="sm:max-w-md">
              <NewSetForm onSaved={() => setNewSetOpen(false)} />
            </DialogContent>
          </Dialog>
          <Dialog open={createOpen} onOpenChange={setCreateOpen}>
            <DialogTrigger
              render={
                <Button size="sm" className="shrink-0">
                  <Plus className="size-4" />
                  <span className="hidden sm:inline">New Memory</span>
                  <span className="sm:hidden">New</span>
                </Button>
              }
            />
            <DialogContent className="sm:max-w-lg">
              <MemoryForm setID={selectedSet} onSaved={() => setCreateOpen(false)} />
            </DialogContent>
          </Dialog>
        </div>
      </div>

      {/* Set selector */}
      {sets && sets.length > 1 && (
        <div className="flex flex-wrap gap-2 items-center">
          <Layers className="size-4 text-muted-foreground shrink-0" />
          {sets.map((s) => (
            <button
              key={s.id}
              onClick={() => { setSelectedSet(s.id); setSelectedPalace(''); setSearchResults(null); }}
              className={cn(
                'px-3 py-1.5 rounded-lg text-sm font-medium transition-colors min-h-[36px] flex items-center gap-1.5',
                selectedSet === s.id
                  ? 'bg-primary text-primary-foreground'
                  : 'bg-muted text-muted-foreground hover:bg-accent hover:text-foreground'
              )}
            >
              {s.name}
              {s.is_default && <Badge variant="outline" className="text-[10px] py-0 px-1">default</Badge>}
            </button>
          ))}
        </div>
      )}

      {/* Search bar */}
      <div className="flex gap-2">
        <div className="relative flex-1">
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 size-4 text-muted-foreground" />
          <Input
            placeholder="Search memories..."
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && handleSearch()}
            className="pl-9"
          />
          {searchResults && (
            <Button
              variant="ghost"
              size="icon-xs"
              className="absolute right-1 top-1/2 -translate-y-1/2"
              onClick={() => { setSearchResults(null); setSearchQuery(''); }}
            >
              <X className="size-4" />
            </Button>
          )}
        </div>
        <Button variant="secondary" size="sm" onClick={handleSearch}>
          <Search className="size-4" />
          <span className="hidden sm:inline">Search</span>
        </Button>
      </div>

      {/* Palace filter pills */}
      <div className="flex flex-wrap gap-2">
        <button
          onClick={() => setSelectedPalace('')}
          className={cn(
            'px-3 py-1.5 rounded-lg text-sm font-medium transition-colors min-h-[36px]',
            selectedPalace === ''
              ? 'bg-primary text-primary-foreground'
              : 'bg-muted text-muted-foreground hover:bg-accent hover:text-foreground'
          )}
        >
          All ({totalMemories})
        </button>
        {palaceList.map((p) => (
          <button
            key={p.palace}
            onClick={() => setSelectedPalace(p.palace)}
            className={cn(
              'px-3 py-1.5 rounded-lg text-sm font-medium transition-colors min-h-[36px]',
              selectedPalace === p.palace
                ? 'bg-primary text-primary-foreground'
                : 'bg-muted text-muted-foreground hover:bg-accent hover:text-foreground'
            )}
          >
            {p.palace} ({p.count})
          </button>
        ))}
      </div>

      {/* Memory list */}
      {displayMemories.length === 0 ? (
        <Card>
          <CardContent className="p-8 sm:p-12 text-center flex flex-col items-center gap-3">
            <Brain className="size-10 text-muted-foreground/50" />
            <p className="text-muted-foreground text-sm">
              {searchResults ? 'No memories found.' : 'No memories yet. Create your first memory to get started.'}
            </p>
          </CardContent>
        </Card>
      ) : (
        <div className="space-y-3">
          {displayMemories.map((mem) => (
            <MemoryCard
              key={mem.id}
              memory={mem}
              onDelete={() => deleteMutation.mutate(mem.id)}
              onEdit={() => setEditing(mem)}
            />
          ))}
        </div>
      )}

      {/* Edit dialog */}
      {editing && (
        <Dialog open onOpenChange={(open) => !open && setEditing(null)}>
          <DialogContent className="sm:max-w-lg">
            <MemoryForm memory={editing} setID={selectedSet} onSaved={() => setEditing(null)} />
          </DialogContent>
        </Dialog>
      )}

      {/* Delete set button (non-default only) */}
      {currentSet && !currentSet.is_default && (
        <div className="pt-4 border-t border-border">
          <Button
            variant="ghost"
            size="sm"
            className="text-destructive hover:text-destructive"
            onClick={() => {
              if (confirm(`Delete memory set "${currentSet.name}"? All memories in this set will be permanently deleted.`)) {
                deleteSetMutation.mutate(currentSet.id);
              }
            }}
          >
            <Trash2 className="size-4" />
            Delete "{currentSet.name}" set
          </Button>
        </div>
      )}
    </div>
  );
}

function MemoryCard({
  memory,
  onDelete,
  onEdit,
}: {
  memory: Memory;
  onDelete: () => void;
  onEdit: () => void;
}) {
  const [confirmDelete, setConfirmDelete] = useState(false);

  return (
    <Card>
      <CardContent className="space-y-3">
        {/* Top row: palace, room, importance */}
        <div className="flex items-start justify-between gap-2 flex-wrap">
          <div className="flex items-center gap-2 flex-wrap">
            <Badge variant="secondary">{memory.palace}</Badge>
            {memory.room && (
              <Badge variant="outline">{memory.room}</Badge>
            )}
            {memory.tags.map((tag) => (
              <Badge key={tag} variant="outline" className="text-xs">
                {tag}
              </Badge>
            ))}
          </div>
          <div className="flex items-center gap-1 shrink-0">
            <Button variant="ghost" size="icon-sm" onClick={onEdit}>
              <Pencil className="size-3.5" />
            </Button>
            {confirmDelete ? (
              <div className="flex items-center gap-1">
                <Button variant="destructive" size="xs" onClick={onDelete}>
                  <Check className="size-3" />
                </Button>
                <Button variant="ghost" size="icon-sm" onClick={() => setConfirmDelete(false)}>
                  <X className="size-3.5" />
                </Button>
              </div>
            ) : (
              <Button variant="ghost" size="icon-sm" onClick={() => setConfirmDelete(true)}>
                <Trash2 className="size-3.5" />
              </Button>
            )}
          </div>
        </div>

        {/* Content */}
        <p className="text-sm text-foreground whitespace-pre-wrap break-words">
          {memory.content}
        </p>

        {/* Footer: chronicle metadata */}
        <Separator />
        <div className="flex items-center gap-4 text-xs text-muted-foreground flex-wrap">
          <span className="flex items-center gap-1">
            <Clock className="size-3" />
            {new Date(memory.created_at).toLocaleDateString()}
          </span>
          {memory.last_accessed && (
            <span className="flex items-center gap-1">
              <Eye className="size-3" />
              Accessed {memory.access_count}× — last {new Date(memory.last_accessed).toLocaleDateString()}
            </span>
          )}
          {!memory.last_accessed && memory.access_count > 0 && (
            <span className="flex items-center gap-1">
              <Eye className="size-3" />
              Accessed {memory.access_count}×
            </span>
          )}
          {/* Importance bar */}
          <div className="flex items-center gap-1.5">
            <span className="text-muted-foreground">Importance</span>
            <div className="w-16 h-1.5 bg-muted rounded-full overflow-hidden">
              <div
                className="h-full bg-primary rounded-full"
                style={{ width: `${memory.importance}%` }}
              />
            </div>
          </div>
        </div>
      </CardContent>
    </Card>
  );
}

function MemoryForm({
  memory,
  setID,
  onSaved,
}: {
  memory?: Memory;
  setID: string;
  onSaved: () => void;
}) {
  const queryClient = useQueryClient();
  const [palace, setPalace] = useState(memory?.palace ?? 'general');
  const [room, setRoom] = useState(memory?.room ?? '');
  const [content, setContent] = useState(memory?.content ?? '');
  const [tags, setTags] = useState((memory?.tags ?? []).join(', '));
  const [importance, setImportance] = useState(memory?.importance ?? 50);

  const saveMutation = useMutation({
    mutationFn: async () => {
      const tagArray = tags.split(',').map((t) => t.trim()).filter(Boolean);
      if (memory) {
        return memApi.update(memory.id, {
          palace,
          room,
          content,
          tags: tagArray,
          importance,
        });
      }
      return memApi.create({
        set_id: setID,
        palace,
        room,
        content,
        tags: tagArray,
        importance,
      });
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['memories', setID] });
      queryClient.invalidateQueries({ queryKey: ['palaces', setID] });
      onSaved();
    },
  });

  return (
    <>
      <DialogHeader>
        <DialogTitle>{memory ? 'Edit Memory' : 'New Memory'}</DialogTitle>
        <DialogDescription>
          {memory
            ? 'Update the memory content and metadata.'
            : 'Store a new memory in the memory palace.'}
        </DialogDescription>
      </DialogHeader>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          saveMutation.mutate();
        }}
        className="flex flex-col gap-4"
      >
        <div className="grid grid-cols-2 gap-3">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="palace">Palace</Label>
            <Input
              id="palace"
              value={palace}
              onChange={(e) => setPalace(e.target.value)}
              placeholder="general"
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="room">Room (optional)</Label>
            <Input
              id="room"
              value={room}
              onChange={(e) => setRoom(e.target.value)}
              placeholder=""
            />
          </div>
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="content">Content</Label>
          <Textarea
            id="content"
            value={content}
            onChange={(e) => setContent(e.target.value)}
            placeholder="What do you want to remember?"
            rows={5}
            required
          />
        </div>
        <div className="grid grid-cols-2 gap-3">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="tags">Tags (comma-separated)</Label>
            <Input
              id="tags"
              value={tags}
              onChange={(e) => setTags(e.target.value)}
              placeholder="important, reference"
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="importance">Importance: {importance}</Label>
            <input
              id="importance"
              type="range"
              min={0}
              max={100}
              value={importance}
              onChange={(e) => setImportance(Number(e.target.value))}
              className="w-full h-9 accent-primary"
            />
          </div>
        </div>
        <DialogFooter>
          <DialogClose render={<Button variant="outline" type="button" />}>
            Cancel
          </DialogClose>
          <Button type="submit" disabled={saveMutation.isPending}>
            {saveMutation.isPending ? 'Saving...' : memory ? 'Update' : 'Store'}
          </Button>
        </DialogFooter>
      </form>
    </>
  );
}

function NewSetForm({ onSaved }: { onSaved: () => void }) {
  const queryClient = useQueryClient();
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [error, setError] = useState('');

  const createMutation = useMutation({
    mutationFn: () => setsApi.create({ name, description: description || undefined }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['memory-sets'] });
      queryClient.invalidateQueries({ queryKey: ['servers'] });
      onSaved();
    },
    onError: (err: Error) => setError(err.message),
  });

  return (
    <>
      <DialogHeader>
        <DialogTitle>New Memory Set</DialogTitle>
        <DialogDescription>
          Create a separate memory collection for a specific project, org, or context.
          Each set appears as its own builtin MCP server.
        </DialogDescription>
      </DialogHeader>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          setError('');
          createMutation.mutate();
        }}
        className="flex flex-col gap-4"
      >
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="set-name">Name</Label>
          <Input
            id="set-name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="e.g. Project Alpha, Work, Personal"
            required
          />
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="set-desc">Description (optional)</Label>
          <Input
            id="set-desc"
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            placeholder="What is this memory set for?"
          />
        </div>
        {error && (
          <p className="text-sm text-destructive">{error}</p>
        )}
        <DialogFooter>
          <DialogClose render={<Button variant="outline" type="button" />}>
            Cancel
          </DialogClose>
          <Button type="submit" disabled={createMutation.isPending || !name.trim()}>
            {createMutation.isPending ? 'Creating...' : 'Create Set'}
          </Button>
        </DialogFooter>
      </form>
    </>
  );
}
