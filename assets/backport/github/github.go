/*
Copyright 2022 Gravitational, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package github

import (
	"context"
	"fmt"

	"github.com/gravitational/trace"

	go_github "github.com/google/go-github/v37/github"
	"golang.org/x/oauth2"
)

type Client struct {
	Client *go_github.Client
}

// New returns a new GitHub client.
func New(ctx context.Context, token string) (*Client, error) {
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)
	return &Client{
		Client: go_github.NewClient(oauth2.NewClient(ctx, ts)),
	}, nil
}

// CherryPickCommitsOnBranch cherry picks a list of commits on a given branch.
func (c *Client) CherryPickCommitsOnBranch(ctx context.Context, organization string, repository string, branch *go_github.Branch, commits []*go_github.Commit) error {
	if branch.Name == nil {
		return trace.NotFound("branch name does not exist.")
	}
	if branch.Commit.SHA == nil {
		return trace.NotFound("branch %s HEAD does not exist.", *branch.Name)
	}

	headCommit, err := c.getCommit(ctx, organization, repository, *branch.Commit.SHA)
	if err != nil {
		return trace.Wrap(err)
	}
	branchName := *branch.Name
	for i := 0; i < len(commits); i++ {
		tree, sha, err := c.cherryPickCommit(ctx, organization, repository, branchName, commits[i], headCommit)
		if err != nil {
			defer c.deleteBranch(ctx, organization, repository, branchName)
			return trace.Wrap(err)
		}
		headCommit.SHA = &sha
		headCommit.Tree = tree
	}
	return nil
}

// cherryPickCommit cherry picks a single commit on a branch.
func (c *Client) cherryPickCommit(ctx context.Context, organization string, repository string, branchName string, cherryCommit *go_github.Commit, headBranchCommit *go_github.Commit) (*go_github.Tree, string, error) {
	cherryParent := cherryCommit.Parents[0]
	// Temporarily set the parent of the branch to the parent of the commit
	// to cherry-pick so they are siblings. When git performs the merge, it 
	// detects that the parent of the branch commit we're merging onto matches
	// the parent of the commit we're merging with, and merges a tree of size 1, 
	// containing only the cherry-pick commit.
	err := c.createSiblingCommit(ctx, organization, repository, branchName, headBranchCommit, cherryParent)
	if err != nil {
		return nil, "", trace.Wrap(err)
	}

	// Merging the original cherry pick commit onto the branch.
	merge, err := c.merge(ctx, organization, repository, branchName, *cherryCommit.SHA)
	if err != nil {
		return nil, "", trace.Wrap(err)
	}
	mergeTree := merge.GetTree()

	// Get the updated HEAD commit with the new parent.
	updatedCommit, err := c.getCommit(ctx, organization, repository, *headBranchCommit.SHA)
	if err != nil {
		return nil, "", trace.Wrap(err)
	}
	// Create a new commit with the updated commit as the parent and the merge tree.
	sha, err := c.createCommit(ctx, organization, repository, *cherryCommit.Message, mergeTree, updatedCommit)
	if err != nil {
		return nil, "", trace.Wrap(err)
	}
	// Overwrite the merge commit and its parent on the branch by the created commit.
	// The result will be equivalent to what would have happened with a fast-forward merge.
	err = c.updateBranch(ctx, organization, repository, branchName, sha)
	if err != nil {
		return nil, "", trace.Wrap(err)
	}
	return mergeTree, sha, nil
}

// createSiblingCommit creates a commit with the passed in commit's tree and parent
// and updates the passed in branch to point at that commit.
func (c *Client) createSiblingCommit(ctx context.Context, organization string, repository string, branchName string, branchHeadCommit *go_github.Commit, cherryParent *go_github.Commit) error {
	tree := branchHeadCommit.GetTree()
	// This will be the "temp" commit, commit is lost. Commit message doesn't matter.
	commitSHA, err := c.createCommit(ctx, organization, repository, "temp", tree, cherryParent)
	if err != nil {
		return trace.Wrap(err)
	}
	err = c.updateBranch(ctx, organization, repository, branchName, commitSHA)
	if err != nil {
		return trace.Wrap(err)
	}
	return nil
}

// CreateBranchFrom creates a branch from the passed in branch's HEAD.
func (c *Client) CreateBranchFrom(ctx context.Context, organization string, repository string, branchFromName string, newBranchName string) (*go_github.Branch, error) {
	baseBranch, _, err := c.Client.Repositories.GetBranch(ctx,
		organization,
		repository,
		branchFromName, true)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	newRefBranchName := fmt.Sprintf("%s%s", branchRefPrefix, newBranchName)
	baseBranchSHA := baseBranch.GetCommit().GetSHA()

	ref := &go_github.Reference{
		Ref: &newRefBranchName,
		Object: &go_github.GitObject{
			SHA: &baseBranchSHA, /* SHA to branch from */
		},
	}
	_, _, err = c.Client.Git.CreateRef(ctx, organization, repository, ref)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	targetBranch, _, err := c.Client.Repositories.GetBranch(ctx,
		organization,
		repository,
		newBranchName, true)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	return targetBranch, nil
}

// updateBranch updates a branch.
func (c *Client) updateBranch(ctx context.Context, organization string, repository string, branchName string, sha string) error {
	refName := fmt.Sprintf("%s%s", branchRefPrefix, branchName)
	_, _, err := c.Client.Git.UpdateRef(ctx, organization, repository, &go_github.Reference{
		Ref: &refName,
		Object: &go_github.GitObject{
			SHA: &sha,
		},
	}, true)
	if err != nil {
		return trace.Wrap(err)
	}
	return nil
}

// createCommit creates a new commit.
func (c *Client) createCommit(ctx context.Context, organization string, repository string, commitMessage string, tree *go_github.Tree, parent *go_github.Commit) (string, error) {
	commit, _, err := c.Client.Git.CreateCommit(ctx, organization, repository, &go_github.Commit{
		Message: &commitMessage,
		Tree:    tree,
		Parents: []*go_github.Commit{
			parent,
		},
	})
	if err != nil {
		return "", trace.Wrap(err)
	}
	return commit.GetSHA(), nil
}

// getCommit gets a commit.
func (c *Client) getCommit(ctx context.Context, organization string, repository string, sha string) (*go_github.Commit, error) {
	commit, _, err := c.Client.Git.GetCommit(ctx,
		organization,
		repository,
		sha)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	return commit, nil
}

// merge merges a branch.
func (c *Client) merge(ctx context.Context, organization string, repository string, base string, headCommitSHA string) (*go_github.Commit, error) {
	merge, _, err := c.Client.Repositories.Merge(ctx, organization, repository, &go_github.RepositoryMergeRequest{
		Base: &base,
		Head: &headCommitSHA,
	})
	if err != nil {
		return nil, trace.Wrap(err)
	}
	mergeCommit, err := c.getCommit(ctx, organization, repository, merge.GetSHA())
	if err != nil {
		return nil, trace.Wrap(err)
	}
	return mergeCommit, nil
}

// GetBranchCommits gets commits on a branch.
//
// The only way to list commits for a branch is through RepositoriesService
// and returns type RepositoryCommit which does not contain the commit
// tree. To get the commit trees, GitService is used to get the commits (of
// type Commit) that contain the commit tree.
func (c *Client) GetBranchCommits(ctx context.Context, organization string, repository string, branchName string) ([]*go_github.Commit, error) {
	// Getting RepositoryCommits.
	repoCommits, err := c.getBranchCommits(ctx, organization, repository, branchName)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	// Get the commits that are not on master. No commits will be returned if
	// the pull request from the branch to backport was not squashed and merged
	// or rebased and merged.
	comparison, _, err := c.Client.Repositories.CompareCommits(ctx, organization, repository, "master", branchName)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	// Getting Commits.
	commits := []*go_github.Commit{}
	for _, repoCommit := range repoCommits {
		for _, diffCommit := range comparison.Commits {
			if diffCommit.GetSHA() == repoCommit.GetSHA() {
				commit, err := c.getCommit(ctx,
					organization,
					repository,
					repoCommit.GetSHA())
				if err != nil {
					return nil, trace.Wrap(err)
				}
				if len(commit.Parents) != 1 {
					return nil, trace.Errorf("merge commits are not supported.")
				}
				commits = append(commits, commit)
			}
		}
	}
	return commits, nil
}

// getBranchCommits gets commits on a branch of type go-github.RepositoryCommit.
func (c *Client) getBranchCommits(ctx context.Context, organization string, repository string, branchName string) ([]*go_github.RepositoryCommit, error) {
	var repoCommits []*go_github.RepositoryCommit
	listOpts := go_github.ListOptions{
		Page:    0,
		PerPage: perPage,
	}
	opts := &go_github.CommitsListOptions{SHA: branchName, ListOptions: listOpts}
	for {
		currCommits, resp, err := c.Client.Repositories.ListCommits(ctx,
			organization,
			repository,
			opts)
		if err != nil {
			return nil, trace.Wrap(err)
		}
		repoCommits = append(repoCommits, currCommits...)
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	return repoCommits, nil
}

// deleteBranch deletes a branch.
func (c *Client) deleteBranch(ctx context.Context, organization string, repository string, branchName string) error {
	refName := fmt.Sprintf("%s%s", branchRefPrefix, branchName)
	_, err := c.Client.Git.DeleteRef(ctx, organization, repository, refName)
	if err != nil {
		return trace.Wrap(err)
	}
	return nil
}

// CreatePullRequest creates a pull request.
func (c *Client) CreatePullRequest(ctx context.Context, organization string, repository string, baseBranch string, headBranch string, title string, body string) error {
	autoTitle := fmt.Sprintf("[Auto Backport] %s", title)
	newPR := &go_github.NewPullRequest{
		Title:               &autoTitle,
		Head:                &headBranch,
		Base:                &baseBranch,
		Body:                &body,
		MaintainerCanModify: go_github.Bool(true),
	}
	_, _, err := c.Client.PullRequests.Create(ctx, organization, repository, newPR)
	if err != nil {
		return err
	}
	return nil
}

const (
	backportPRState          = "closed"
	backportMasterBranchName = "master"
)

// GetPullRequestMetadata gets a pull request's title and body by branch name.
func (c *Client) GetPullRequestMetadata(ctx context.Context, organization string, repository string, user string, branchName string) (title string, body string, err error) {
	prBranchName := fmt.Sprintf("%s:%s", user, branchName)
	prs, _, err := c.Client.PullRequests.List(ctx,
		organization,
		repository,
		&go_github.PullRequestListOptions{
			// Get PRs that are closed and whose base is master.
			State: backportPRState,
			Base:  backportMasterBranchName,
			// Head filters pull requests by user and branch name in the format of:
			// "user:ref-name".
			Head: prBranchName,
		})
	if err != nil {
		return "", "", trace.Wrap(err)
	}
	if len(prs) == 0 {
		return "", "", trace.Errorf("pull request for branch %s does not exist", branchName)
	}
	if len(prs) != 1 {
		return "", "", trace.Errorf("found more than 1 pull request for branch %s", branchName)
	}
	pull := prs[0]
	return pull.GetTitle(), pull.GetBody(), nil
}

const (
	// perPage is the number of items per page to request.
	perPage = 100

	// branchRefPrefix is the prefix for a reference that is 
	// pointing to a branch.
	branchRefPrefix = "refs/heads/"
)
