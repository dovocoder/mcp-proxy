import { useState } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { Plus, Trash2, Search, BookOpen, Clock, Eye, Pencil, X, Check, FolderPlus, Link2, Copy, Tag } from 'lucide-react';
import { skills as skillsApi, skillSets as setsApi, type Skill, type SkillSet } from '@/api/client';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '@/components/ui/card';
import { Badge } from '@/components/ui/badge';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Textarea } from '@/components/ui/textarea';
import { Separator } from '@/components/ui/separator';
import { ConfirmDialog } from '@/components/ConfirmDialog';
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

export default function Skills() {
  const queryClient = useQueryClient();
  const [selectedSet, setSelectedSet] = useState<string>('default');
  const [selectedCategory, setSelectedCategory] = useState<string>('');
  const [searchQuery, setSearchQuery] = useState('');
  const [searchResults, setSearchResults] = useState<Skill[] | null>(null);
  const [editing, setEditing] = useState<Skill | null>(null);
  const [createOpen, setCreateOpen] = useState(false);
  const [newSetOpen, setNewSetOpen] = useState(false);
  const [copiedUrl, setCopiedUrl] = useState<string | null>(null);
  const [deleteSetOpen, setDeleteSetOpen] = useState(false);

  const { data: sets } = useQuery({ queryKey: ['skill-sets'], queryFn: setsApi.list });

  const { data: allSkills } = useQuery({
    queryKey: ['skills', selectedSet, selectedCategory],
    queryFn: () => skillsApi.list(selectedSet, selectedCategory || undefined),
  });

  const { data: categories } = useQuery({
    queryKey: ['skill-categories', selectedSet],
    queryFn: () => skillsApi.categories(selectedSet),
  });

  const deleteMutation = useMutation({
    mutationFn: skillsApi.delete,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['skills', selectedSet] });
      queryClient.invalidateQueries({ queryKey: ['skill-categories', selectedSet] });
    },
  });

  const deleteSetMutation = useMutation({
    mutationFn: setsApi.delete,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['skill-sets'] });
      setSelectedSet('default');
    },
  });

  const handleSearch = async () => {
    if (!searchQuery.trim()) {
      setSearchResults(null);
      return;
    }
    try {
      const results = await skillsApi.search(selectedSet, searchQuery);
      setSearchResults(results);
    } catch (err) {
      console.error('[Skill search]', err);
      setSearchResults([]);
    }
  };

  const displaySkills = searchResults ?? allSkills ?? [];
  const categoryList = categories ?? [];
  const totalSkills = categoryList.reduce((sum, c) => sum + c.count, 0);
  const currentSet = sets?.find((s) => s.id === selectedSet);

  // Connection URLs for this skill set
  const origin = typeof window !== 'undefined' ? window.location.origin : '';
  const skillServerId = selectedSet === 'default'
    ? 'builtin-skills'
    : `builtin-skills:${selectedSet}`;
  const mcpUrl = `${origin}/api/servers/${skillServerId}/mcp`;
  const sseUrl = `${origin}/api/servers/${skillServerId}/sse`;

  const copyToClipboard = (text: string) => {
    navigator.clipboard.writeText(text);
    setCopiedUrl(text);
    setTimeout(() => setCopiedUrl(null), 2000);
  };

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between gap-3">
        <div className="min-w-0">
          <h1 className="text-xl sm:text-2xl font-bold text-foreground">Skills</h1>
          <p className="text-muted-foreground mt-1 text-sm">
            {currentSet?.description || `Skill set: ${currentSet?.name ?? 'Default'}`}
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
                  <span className="hidden sm:inline">New Skill</span>
                  <span className="sm:hidden">New</span>
                </Button>
              }
            />
            <DialogContent className="sm:max-w-2xl">
              <SkillForm setID={selectedSet} onSaved={() => setCreateOpen(false)} />
            </DialogContent>
          </Dialog>
        </div>
      </div>

      {/* Set selector */}
      {sets && sets.length > 1 && (
        <div className="flex flex-wrap gap-2 items-center">
          {sets.map((s) => (
            <button
              key={s.id}
              onClick={() => { setSelectedSet(s.id); setSelectedCategory(''); setSearchResults(null); }}
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

      {/* Connection URLs for this skill set */}
      <Card>
        <CardHeader>
          <div className="flex items-center gap-2">
            <Link2 className="size-4 text-primary shrink-0" />
            <div>
              <CardTitle className="text-base">Connection URLs</CardTitle>
              <CardDescription>
                Connect MCP clients to this skill set
                {currentSet && currentSet.name !== 'skills' ? ` (${currentSet.name})` : ''}.
              </CardDescription>
            </div>
          </div>
        </CardHeader>
        <CardContent className="space-y-3">
          <div className="flex items-center gap-2">
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
          <Separator />
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
        </CardContent>
      </Card>

      {/* Search bar */}
      <div className="flex gap-2">
        <div className="relative flex-1">
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 size-4 text-muted-foreground" />
          <Input
            placeholder="Search skills..."
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

      {/* Category filter pills */}
      <div className="flex flex-wrap gap-2">
        <button
          onClick={() => setSelectedCategory('')}
          className={cn(
            'px-3 py-1.5 rounded-lg text-sm font-medium transition-colors min-h-[36px]',
            selectedCategory === ''
              ? 'bg-primary text-primary-foreground'
              : 'bg-muted text-muted-foreground hover:bg-accent hover:text-foreground'
          )}
        >
          All ({totalSkills})
        </button>
        {categoryList.map((c) => (
          <button
            key={c.category}
            onClick={() => setSelectedCategory(c.category)}
            className={cn(
              'px-3 py-1.5 rounded-lg text-sm font-medium transition-colors min-h-[36px]',
              selectedCategory === c.category
                ? 'bg-primary text-primary-foreground'
                : 'bg-muted text-muted-foreground hover:bg-accent hover:text-foreground'
            )}
          >
            {c.category} ({c.count})
          </button>
        ))}
      </div>

      {/* Skill list */}
      {displaySkills.length === 0 ? (
        <Card>
          <CardContent className="p-8 sm:p-12 text-center flex flex-col items-center gap-3">
            <BookOpen className="size-10 text-muted-foreground/50" />
            <p className="text-muted-foreground text-sm">
              {searchResults ? 'No skills found.' : 'No skills yet. Create your first skill to get started.'}
            </p>
          </CardContent>
        </Card>
      ) : (
        <div className="space-y-3">
          {displaySkills.map((sk) => (
            <SkillCard
              key={sk.id}
              skill={sk}
              onDelete={() => deleteMutation.mutate(sk.id)}
              onEdit={() => setEditing(sk)}
            />
          ))}
        </div>
      )}

      {/* Edit dialog */}
      {editing && (
        <Dialog open onOpenChange={(open) => !open && setEditing(null)}>
          <DialogContent className="sm:max-w-2xl">
            <SkillForm skill={editing} setID={selectedSet} onSaved={() => setEditing(null)} />
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
            onClick={() => setDeleteSetOpen(true)}
          >
            <Trash2 className="size-4" />
            Delete "{currentSet.name}" set
          </Button>
        </div>
      )}

      <ConfirmDialog
        open={deleteSetOpen}
        onOpenChange={setDeleteSetOpen}
        title="Delete Skill Set"
        description="Delete"
        itemName={currentSet?.name}
        confirmText="Delete Set"
        loading={deleteSetMutation.isPending}
        onConfirm={() => {
          if (currentSet) {
            deleteSetMutation.mutate(currentSet.id, {
              onSuccess: () => {
                setDeleteSetOpen(false);
                setSelectedSet('default');
              },
            });
          }
        }}
      />
    </div>
  );
}

function SkillCard({
  skill,
  onDelete,
  onEdit,
}: {
  skill: Skill;
  onDelete: () => void;
  onEdit: () => void;
}) {
  const [deleteOpen, setDeleteOpen] = useState(false);

  return (
    <>
    <Card>
      <CardContent className="space-y-3">
        {/* Top row: name, category, version */}
        <div className="flex items-start justify-between gap-2 flex-wrap">
          <div className="flex items-center gap-2 flex-wrap min-w-0">
            <Badge variant="secondary">{skill.category}</Badge>
            <span className="font-mono text-sm font-medium text-foreground truncate">{skill.name}</span>
            <Badge variant="outline" className="text-xs">v{skill.version}</Badge>
            {skill.tags.map((tag) => (
              <Badge key={tag} variant="outline" className="text-xs">
                <Tag className="size-2.5 mr-0.5" />
                {tag}
              </Badge>
            ))}
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
        {skill.description && (
          <p className="text-sm text-muted-foreground">
            {skill.description}
          </p>
        )}

        {/* Content preview */}
        <p className="text-xs text-muted-foreground font-mono whitespace-pre-wrap break-words line-clamp-3 overflow-hidden">
          {skill.content}
        </p>

        {/* Footer: metadata */}
        <Separator />
        <div className="flex items-center gap-4 text-xs text-muted-foreground flex-wrap">
          <span className="flex items-center gap-1">
            <Clock className="size-3" />
            {new Date(skill.created_at).toLocaleDateString()}
          </span>
          {skill.last_accessed && (
            <span className="flex items-center gap-1">
              <Eye className="size-3" />
              Loaded {skill.access_count}× — last {new Date(skill.last_accessed).toLocaleDateString()}
            </span>
          )}
          {!skill.last_accessed && skill.access_count > 0 && (
            <span className="flex items-center gap-1">
              <Eye className="size-3" />
              Loaded {skill.access_count}×
            </span>
          )}
        </div>
      </CardContent>
    </Card>
    <ConfirmDialog
      open={deleteOpen}
      onOpenChange={setDeleteOpen}
      title="Delete Skill"
      description="Delete"
      itemName={skill.name}
      confirmText="Delete"
      onConfirm={() => {
        onDelete();
        setDeleteOpen(false);
      }}
    />
    </>
  );
}

function SkillForm({
  skill,
  setID,
  onSaved,
}: {
  skill?: Skill;
  setID: string;
  onSaved: () => void;
}) {
  const queryClient = useQueryClient();
  const [name, setName] = useState(skill?.name ?? '');
  const [description, setDescription] = useState(skill?.description ?? '');
  const [content, setContent] = useState(skill?.content ?? '');
  const [category, setCategory] = useState(skill?.category ?? 'general');
  const [tags, setTags] = useState((skill?.tags ?? []).join(', '));
  const [version, setVersion] = useState(skill?.version ?? '1.0.0');

  const saveMutation = useMutation({
    mutationFn: async () => {
      const tagArray = tags.split(',').map((t) => t.trim()).filter(Boolean);
      if (skill) {
        return skillsApi.update(skill.id, {
          name,
          description,
          content,
          category,
          tags: tagArray,
          version,
        });
      }
      return skillsApi.create({
        name,
        description,
        content,
        category,
        tags: tagArray,
        version,
      }, setID);
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['skills', setID] });
      queryClient.invalidateQueries({ queryKey: ['skill-categories', setID] });
      onSaved();
    },
  });

  return (
    <>
      <DialogHeader>
        <DialogTitle>{skill ? 'Edit Skill' : 'New Skill'}</DialogTitle>
        <DialogDescription>
          {skill
            ? 'Update the skill content and metadata.'
            : 'Create a reusable skill (SKILL.md format).'}
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
            <Label htmlFor="skill-name">Name</Label>
            <Input
              id="skill-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="deploy-dokploy"
              className="font-mono"
              required
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="skill-category">Category</Label>
            <Input
              id="skill-category"
              value={category}
              onChange={(e) => setCategory(e.target.value)}
              placeholder="devops"
            />
          </div>
        </div>
        <div className="grid grid-cols-2 gap-3">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="skill-version">Version</Label>
            <Input
              id="skill-version"
              value={version}
              onChange={(e) => setVersion(e.target.value)}
              placeholder="1.0.0"
              className="font-mono"
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="skill-tags">Tags (comma-separated)</Label>
            <Input
              id="skill-tags"
              value={tags}
              onChange={(e) => setTags(e.target.value)}
              placeholder="docker, deploy"
            />
          </div>
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="skill-description">Description</Label>
          <Input
            id="skill-description"
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            placeholder="Deploy a containerized app to Dokploy"
          />
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="skill-content">Content (SKILL.md)</Label>
          <Textarea
            id="skill-content"
            value={content}
            onChange={(e) => setContent(e.target.value)}
            placeholder="## Trigger&#10;When deploying to Dokploy...&#10;&#10;## Steps&#10;1. ..."
            className="font-mono text-sm min-h-[200px]"
            required
          />
        </div>
        <DialogFooter>
          <DialogClose render={<Button type="button" variant="outline">Cancel</Button>} />
          <Button type="submit" disabled={saveMutation.isPending}>
            {saveMutation.isPending ? 'Saving...' : skill ? 'Update Skill' : 'Create Skill'}
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

  const saveMutation = useMutation({
    mutationFn: () => setsApi.create({ name, description }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['skill-sets'] });
      onSaved();
    },
  });

  return (
    <>
      <DialogHeader>
        <DialogTitle>New Skill Set</DialogTitle>
        <DialogDescription>
          Create a new collection of skills for a specific project or context.
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
          <Label htmlFor="set-name">Name</Label>
          <Input
            id="set-name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="Project Alpha Skills"
            required
          />
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="set-description">Description (optional)</Label>
          <Input
            id="set-description"
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            placeholder="Skills for the Alpha project"
          />
        </div>
        <DialogFooter>
          <DialogClose render={<Button type="button" variant="outline">Cancel</Button>} />
          <Button type="submit" disabled={saveMutation.isPending || !name}>
            {saveMutation.isPending ? 'Creating...' : 'Create Set'}
          </Button>
        </DialogFooter>
      </form>
    </>
  );
}
