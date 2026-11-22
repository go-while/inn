/* Global storage shared by innd and the innd unit tests. */

#include "portable/system.h"

#include "innd.h"

bool AnyIncoming;
bool ICDneedsetup;
bool NeedHeaders;
bool NeedOverview;
bool NeedPath;
bool NeedStoredGroup;
bool NeedReplicdata;
struct buffer Path;
struct buffer Pathalias;
struct buffer Pathcluster;
char *ModeReason;
char *NNRPReason;
char *Reservation;
char *RejectReason;
unsigned long ICDactivedirty;
int MaxOutgoing;
int nGroups;
SITEIDX nSites;
volatile sig_atomic_t PROCneedscan;
NEWSGROUP **GroupPointers;
NEWSGROUP *Groups;
volatile sig_atomic_t GotTerminate;
SITE *Sites;
SITE ME;
struct timeval TimeOut;
struct timeval Now;
char *NCgreeting;
struct history *History;
