import { Component, type ErrorInfo, type ReactNode } from 'react';
import { AlertCircle, RotateCcw } from 'lucide-react';
import { Button } from '@/components/ui/button';

interface Props {
  children: ReactNode;
}

interface State {
  hasError: boolean;
  error: Error | null;
}

export class ErrorBoundary extends Component<Props, State> {
  state: State = { hasError: false, error: null };

  static getDerivedStateFromError(error: Error): State {
    return { hasError: true, error };
  }

  componentDidCatch(error: Error, errorInfo: ErrorInfo) {
    console.error('[ErrorBoundary]', error, errorInfo);
  }

  handleReset = () => {
    this.setState({ hasError: false, error: null });
  };

  render() {
    if (this.state.hasError) {
      return (
        <div className="flex min-h-screen items-center justify-center bg-background p-4">
          <div className="w-full max-w-md space-y-4 text-center">
            <AlertCircle className="mx-auto size-12 text-destructive" />
            <h1 className="text-xl font-semibold text-foreground">
              Something went wrong
            </h1>
            <p className="text-sm text-muted-foreground">
              {this.state.error?.message || 'An unexpected error occurred.'}
            </p>
            <Button onClick={this.handleReset} variant="outline" size="sm">
              <RotateCcw className="size-3.5 mr-1.5" />
              Try again
            </Button>
          </div>
        </div>
      );
    }
    return this.props.children;
  }
}
