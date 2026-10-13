import { Component, type ReactNode } from 'react';

/** Shows a message instead of a blank page when a page fails to render. */
export class ErrorBoundary extends Component<{ children: ReactNode; resetKey?: string }, { error: Error | null }> {
  state = { error: null as Error | null };

  static getDerivedStateFromError(error: Error) {
    return { error };
  }

  componentDidUpdate(prev: { resetKey?: string }) {
    // Navigating elsewhere gives the next page a fresh start.
    if (prev.resetKey !== this.props.resetKey && this.state.error) this.setState({ error: null });
  }

  render() {
    if (!this.state.error) return this.props.children;
    return (
      <div className="card"><div className="pane">
        <div className="msg bad">This page failed to display: {this.state.error.message}</div>
        <div className="row"><button onClick={() => this.setState({ error: null })}>Try again</button></div>
      </div></div>
    );
  }
}
