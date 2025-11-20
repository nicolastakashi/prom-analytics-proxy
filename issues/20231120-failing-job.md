### Issue: Failing GitHub Actions Job

#### Job ID: 55876589013

We are experiencing some failures in the GitHub Actions job due to the following issues:

1. **Git Error:** 
   - The job fails with the error: `fatal: detected dubious ownership in repository`. This is caused by recent security changes related to `safe.directory`. 
   - **Recommended Fix:** Add a step to mark the repository as a safe directory by including the following command before any build steps:
     ```sh
     git config --global --add safe.directory "$GITHUB_WORKSPACE"
     ```

2. **Cache Error:** 
   - We are also encountering cache errors indicated by `BlobNotFound` when using the GitHub Actions cache. This cache error is typically transient.
   - **Suggestions:** You may want to retry the job or, if it continues to fail, temporarily comment out the cache step in your workflow.

#### Reference Run:  
[GitHub Actions Run #19518579978](https://github.com/nicolastakashi/prom-analytics-proxy/actions/runs/19518579978/job/55876589013)