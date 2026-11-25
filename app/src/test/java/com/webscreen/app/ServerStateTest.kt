package com.webscreen.app

import org.junit.After
import org.junit.Before
import org.junit.Test
import org.junit.Assert.*

class ServerStateTest {
    @Before
    @After
    fun reset() {
        assertTrue(ServerState.transition(ServerState.status.revision, ServerState.Phase.STOPPED))
    }

    @Test
    fun readyDuringStartupWinsOverLateFailure() {
        val revision = ServerState.requestStart()!!
        val observed = ServerState.status
        assertTrue(ServerState.reconcile(observed, true))
        assertEquals(ServerState.Phase.RUNNING, ServerState.status.phase)
        assertFalse(ServerState.transition(revision, ServerState.Phase.ERROR, "late failure"))
    }

    @Test
    fun earlyNegativeProbeDoesNotCancelStartup() {
        ServerState.requestStart()
        val observed = ServerState.status
        assertFalse(ServerState.reconcile(observed, false, observed.sinceNanos + 1))
        assertEquals(ServerState.Phase.STARTING, ServerState.status.phase)
    }

    @Test
    fun staleStartingExpiresAndCanRecover() {
        ServerState.requestStart()
        val observed = ServerState.status
        assertTrue(ServerState.reconcile(observed, false, observed.sinceNanos + 60_000_000_000L))
        assertEquals(ServerState.Phase.ERROR, ServerState.status.phase)
        assertTrue(ServerState.reconcile(ServerState.status, true))
        assertEquals(ServerState.Phase.RUNNING, ServerState.status.phase)
    }

    @Test
    fun stopIsNotOverwrittenByOldOrNewPositiveProbe() {
        ServerState.requestStart()
        val starting = ServerState.status
        ServerState.requestStop()
        assertFalse(ServerState.reconcile(starting, true))
        assertFalse(ServerState.reconcile(ServerState.status, true))
        assertEquals(ServerState.Phase.STOPPING, ServerState.status.phase)
    }

    @Test
    fun detachedServerRecoversFromStoppedOrError() {
        assertTrue(ServerState.reconcile(ServerState.status, true))
        assertEquals(ServerState.Phase.RUNNING, ServerState.status.phase)
        ServerState.transition(ServerState.status.revision, ServerState.Phase.ERROR, "old error")
        assertTrue(ServerState.reconcile(ServerState.status, true))
        assertNull(ServerState.status.error)
    }
}
